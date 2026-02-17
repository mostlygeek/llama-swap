package proxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type BenchyJobStatus string

const (
	benchyStatusRunning  BenchyJobStatus = "running"
	benchyStatusDone     BenchyJobStatus = "done"
	benchyStatusError    BenchyJobStatus = "error"
	benchyStatusCanceled BenchyJobStatus = "canceled"
)

var (
	benchyDefaultPP = []int{512, 2048, 8192}
	benchyDefaultTG = []int{32, 128}
)

const (
	benchyDefaultRuns      = 5
	benchyMaxOutputBytes   = 1024 * 1024 // 1MB per stream
	benchyMaxJobsInMemory  = 25
	benchyEnvCmd           = "LLAMA_BENCHY_CMD"
	benchyEnvDisableRunner = "LLAMA_BENCHY_DISABLE"
	benchyEnvOutputDir     = "LLAMA_BENCHY_OUTPUT_DIR"
	benchyEnvPyShimDir     = "LLAMA_SWAP_BENCHY_PY_SHIM_DIR"
	benchyEnvSwebenchShim  = "LLAMA_SWAP_SWEBENCH_TEXT_COMPAT"
	benchyDefaultOutputDir = "/tmp/llama-benchy-runs"
)

const (
	benchyForkGitRef             = "git+https://github.com/christopherowen/llama-benchy.git@intelligence"
	benchyForkIntelligenceReqRef = "llama-benchy[intelligence] @ git+https://github.com/christopherowen/llama-benchy.git@intelligence"
)

var benchyIntelligencePluginChoices = map[string]struct{}{
	"all":               {},
	"core6":             {},
	"mmlu":              {},
	"arc-c":             {},
	"hellaswag":         {},
	"winogrande":        {},
	"gsm8k":             {},
	"truthfulqa":        {},
	"ifeval":            {},
	"evalplus":          {},
	"terminal_bench":    {},
	"aider":             {},
	"swebench_verified": {},
}

var benchyPluginsRequireCodeExec = map[string]struct{}{
	"all":               {},
	"evalplus":          {},
	"terminal_bench":    {},
	"aider":             {},
	"swebench_verified": {},
}

type BenchyJob struct {
	ID                  string   `json:"id"`
	Model               string   `json:"model"`
	QueueModels         []string `json:"queueModels,omitempty"`
	QueueCurrentIndex   int      `json:"queueCurrentIndex,omitempty"`
	QueueCurrentModel   string   `json:"queueCurrentModel,omitempty"`
	QueueCompletedCount int      `json:"queueCompletedCount,omitempty"`
	Tokenizer           string   `json:"tokenizer"`
	BaseURL             string   `json:"baseUrl"`
	PP                  []int    `json:"pp"`
	TG                  []int    `json:"tg"`
	Depth               []int    `json:"depth,omitempty"`
	Concurrency         []int    `json:"concurrency,omitempty"`
	Runs                int      `json:"runs"`
	LatencyMode         string   `json:"latencyMode,omitempty"`
	NoCache             bool     `json:"noCache,omitempty"`
	NoWarmup            bool     `json:"noWarmup,omitempty"`
	AdaptPrompt         *bool    `json:"adaptPrompt,omitempty"`
	EnablePrefixCaching bool     `json:"enablePrefixCaching,omitempty"`
	EnableIntelligence  bool     `json:"enableIntelligence,omitempty"`
	IntelligencePlugins []string `json:"intelligencePlugins,omitempty"`
	AllowCodeExec       bool     `json:"allowCodeExec,omitempty"`
	DatasetCacheDir     string   `json:"datasetCacheDir,omitempty"`
	OutputDir           string   `json:"outputDir,omitempty"`
	MaxConcurrent       *int     `json:"maxConcurrent,omitempty"`
	// TrustRemoteCode controls whether we auto-accept the HuggingFace "custom code" prompt for some tokenizers.
	// This only affects local tokenizer loading, not the remote server.
	TrustRemoteCode bool `json:"trustRemoteCode,omitempty"`

	Status     BenchyJobStatus `json:"status"`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
	ExitCode   *int            `json:"exitCode,omitempty"`

	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	Error  string `json:"error,omitempty"`
}

type benchyStartRequest struct {
	Model               string   `json:"model"`
	QueueModels         []string `json:"queueModels,omitempty"`
	Tokenizer           string   `json:"tokenizer,omitempty"`
	BaseURL             string   `json:"baseUrl,omitempty"`
	PP                  []int    `json:"pp,omitempty"`
	TG                  []int    `json:"tg,omitempty"`
	Depth               []int    `json:"depth,omitempty"`
	Concurrency         []int    `json:"concurrency,omitempty"`
	Runs                int      `json:"runs,omitempty"`
	LatencyMode         string   `json:"latencyMode,omitempty"`
	NoCache             bool     `json:"noCache,omitempty"`
	NoWarmup            bool     `json:"noWarmup,omitempty"`
	AdaptPrompt         *bool    `json:"adaptPrompt,omitempty"`
	EnablePrefixCaching bool     `json:"enablePrefixCaching,omitempty"`
	EnableIntelligence  bool     `json:"enableIntelligence,omitempty"`
	IntelligencePlugins []string `json:"intelligencePlugins,omitempty"`
	AllowCodeExec       bool     `json:"allowCodeExec,omitempty"`
	DatasetCacheDir     string   `json:"datasetCacheDir,omitempty"`
	OutputDir           string   `json:"outputDir,omitempty"`
	MaxConcurrent       *int     `json:"maxConcurrent,omitempty"`
	// TrustRemoteCode (when set) overrides metadata-based defaulting.
	TrustRemoteCode *bool `json:"trustRemoteCode,omitempty"`
}

type benchyRunOptions struct {
	PP                  []int
	TG                  []int
	Depth               []int
	Concurrency         []int
	Runs                int
	LatencyMode         string
	NoCache             bool
	NoWarmup            bool
	AdaptPrompt         *bool
	EnablePrefixCaching bool
	EnableIntelligence  bool
	IntelligencePlugins []string
	AllowCodeExec       bool
	DatasetCacheDir     string
	OutputDir           string
	MaxConcurrent       *int
	TrustRemoteCode     bool
}

type benchyStartResponse struct {
	ID string `json:"id"`
}

type benchyResolvedModel struct {
	RequestedModel string
	RealModelName  string
}

// apiStartBenchy starts a llama-benchy run in the background and returns a job id.
func (pm *ProxyManager) apiStartBenchy(c *gin.Context) {
	if v := strings.TrimSpace(os.Getenv(benchyEnvDisableRunner)); v == "1" || strings.EqualFold(v, "true") {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "benchy is disabled"})
		return
	}

	var req benchyStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}

	queueModels, err := normalizeBenchyQueueModels(req.Model, req.QueueModels)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resolvedModels := make([]benchyResolvedModel, 0, len(queueModels))
	for _, requestedModel := range queueModels {
		realModelName, found := pm.config.RealModelName(requestedModel)
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("model not found: %s", requestedModel)})
			return
		}
		resolvedModels = append(resolvedModels, benchyResolvedModel{
			RequestedModel: requestedModel,
			RealModelName:  realModelName,
		})
	}

	baseURLRaw := strings.TrimSpace(req.BaseURL)
	userProvidedBaseURL := baseURLRaw != ""
	if baseURLRaw == "" {
		// Default to this llama-swap instance (/v1). This allows benchy runs to use
		// normal model routing/swap behavior without requiring per-model upstream URLs.
		baseURLRaw = defaultBenchyBaseURLFromRequest(c)
	}
	baseURL, err := normalizeBenchyBaseURL(baseURLRaw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	pp := append([]int{}, req.PP...)
	if len(pp) == 0 {
		pp = append([]int{}, benchyDefaultPP...)
	}
	if err := validatePositiveIntSlice("pp", pp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tg := append([]int{}, req.TG...)
	if len(tg) == 0 {
		tg = append([]int{}, benchyDefaultTG...)
	}
	if err := validatePositiveIntSlice("tg", tg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	depth := append([]int{}, req.Depth...)
	if len(depth) > 0 {
		if err := validateNonNegativeIntSlice("depth", depth); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	concurrency := append([]int{}, req.Concurrency...)
	if len(concurrency) > 0 {
		if err := validatePositiveIntSlice("concurrency", concurrency); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	runs := req.Runs
	if runs <= 0 {
		runs = benchyDefaultRuns
	}
	if runs <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "runs must be > 0"})
		return
	}
	latencyMode, err := normalizeBenchyLatencyMode(req.LatencyMode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	intelligencePlugins, err := normalizeIntelligencePlugins(req.IntelligencePlugins)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	enableIntelligence := req.EnableIntelligence || len(intelligencePlugins) > 0
	if enableIntelligence && len(intelligencePlugins) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "intelligence mode requires at least one plugin"})
		return
	}
	if enableIntelligence && !req.AllowCodeExec {
		if plugin, ok := firstPluginThatRequiresCodeExec(intelligencePlugins); ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("plugin %q requires allowCodeExec=true", plugin)})
			return
		}
	}

	outputDir := expandLeadingTilde(strings.TrimSpace(req.OutputDir))
	if enableIntelligence && outputDir == "" {
		outputDir = defaultBenchyOutputDir()
	}

	datasetCacheDir := expandLeadingTilde(strings.TrimSpace(req.DatasetCacheDir))
	var maxConcurrent *int
	if req.MaxConcurrent != nil {
		if *req.MaxConcurrent <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "maxConcurrent must be > 0"})
			return
		}
		v := *req.MaxConcurrent
		maxConcurrent = &v
	}

	fixedTokenizer := strings.TrimSpace(req.Tokenizer)

	var adaptPrompt *bool
	if req.AdaptPrompt != nil {
		v := *req.AdaptPrompt
		adaptPrompt = &v
	}
	runOptions := benchyRunOptions{
		PP:                  pp,
		TG:                  tg,
		Depth:               depth,
		Concurrency:         concurrency,
		Runs:                runs,
		LatencyMode:         latencyMode,
		NoCache:             req.NoCache,
		NoWarmup:            req.NoWarmup,
		AdaptPrompt:         adaptPrompt,
		EnablePrefixCaching: req.EnablePrefixCaching,
		EnableIntelligence:  enableIntelligence,
		IntelligencePlugins: intelligencePlugins,
		AllowCodeExec:       req.AllowCodeExec,
		DatasetCacheDir:     datasetCacheDir,
		OutputDir:           outputDir,
		MaxConcurrent:       maxConcurrent,
	}

	// If auth is enabled, reuse the validated key from the incoming request.
	apiKey := ""
	if len(pm.config.RequiredAPIKeys) > 0 {
		if v, ok := c.Get(ctxKeyAPIKey); ok {
			if s, ok := v.(string); ok {
				apiKey = s
			}
		}
		if apiKey == "" {
			// This should be impossible because apiKeyAuth() already validated the request.
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing API key in request context"})
			return
		}
	}

	jobID, err := newBenchyJobID()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create job id"})
		return
	}

	tokenizer := fixedTokenizer
	if tokenizer == "" {
		tokenizer = pm.defaultBenchyTokenizer(resolvedModels[0].RealModelName)
	}

	job := &BenchyJob{
		ID:                  jobID,
		Model:               resolvedModels[0].RequestedModel,
		Tokenizer:           tokenizer,
		BaseURL:             baseURL,
		PP:                  append([]int{}, runOptions.PP...),
		TG:                  append([]int{}, runOptions.TG...),
		Depth:               append([]int{}, runOptions.Depth...),
		Concurrency:         append([]int{}, runOptions.Concurrency...),
		Runs:                runOptions.Runs,
		LatencyMode:         runOptions.LatencyMode,
		NoCache:             runOptions.NoCache,
		NoWarmup:            runOptions.NoWarmup,
		AdaptPrompt:         runOptions.AdaptPrompt,
		EnablePrefixCaching: runOptions.EnablePrefixCaching,
		EnableIntelligence:  runOptions.EnableIntelligence,
		IntelligencePlugins: append([]string{}, runOptions.IntelligencePlugins...),
		AllowCodeExec:       runOptions.AllowCodeExec,
		DatasetCacheDir:     runOptions.DatasetCacheDir,
		OutputDir:           runOptions.OutputDir,
		MaxConcurrent:       runOptions.MaxConcurrent,
		TrustRemoteCode:     pm.resolveBenchyTrustRemoteCode(resolvedModels[0].RealModelName, req.TrustRemoteCode),
		Status:              benchyStatusRunning,
		StartedAt:           time.Now(),
	}
	if len(resolvedModels) > 1 {
		job.QueueModels = append([]string{}, queueModels...)
		job.QueueCurrentIndex = 0
		job.QueueCurrentModel = resolvedModels[0].RequestedModel
		job.QueueCompletedCount = 0
	}

	ctx, cancel := context.WithCancel(pm.shutdownCtx)
	pm.benchyMu.Lock()
	pm.benchyJobs[jobID] = job
	pm.benchyCancels[jobID] = cancel
	pm.benchyMu.Unlock()

	if len(resolvedModels) > 1 {
		go pm.runBenchyQueueJob(ctx, jobID, resolvedModels, fixedTokenizer, baseURL, userProvidedBaseURL, apiKey, runOptions, req.TrustRemoteCode)
	} else {
		servedModelName := pm.resolveBenchyServedModelName(
			resolvedModels[0].RequestedModel,
			resolvedModels[0].RealModelName,
			tokenizer,
			userProvidedBaseURL,
		)
		displayModelName := benchyDisplayModelName(servedModelName, tokenizer)
		singleOpts := runOptions
		singleOpts.TrustRemoteCode = job.TrustRemoteCode
		go pm.runBenchyJob(ctx, jobID, displayModelName, servedModelName, tokenizer, baseURL, apiKey, singleOpts)
	}

	c.JSON(http.StatusOK, benchyStartResponse{ID: jobID})
}

func (pm *ProxyManager) apiGetBenchyJob(c *gin.Context) {
	jobID := strings.TrimSpace(c.Param("id"))
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing job id"})
		return
	}

	pm.benchyMu.Lock()
	job, ok := pm.benchyJobs[jobID]
	pm.benchyMu.Unlock()
	if !ok || job == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	c.JSON(http.StatusOK, job)
}

func (pm *ProxyManager) apiCancelBenchyJob(c *gin.Context) {
	jobID := strings.TrimSpace(c.Param("id"))
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing job id"})
		return
	}

	pm.benchyMu.Lock()
	cancel, okCancel := pm.benchyCancels[jobID]
	job, okJob := pm.benchyJobs[jobID]
	pm.benchyMu.Unlock()

	if !okJob || job == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}
	if !okCancel || cancel == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "job is not cancelable"})
		return
	}

	cancel()
	c.JSON(http.StatusOK, gin.H{"status": "canceling"})
}

func (pm *ProxyManager) runBenchyJob(ctx context.Context, jobID, displayModelName, servedModelName, tokenizer, baseURL, apiKey string, opts benchyRunOptions) {
	status, exitCode, err := pm.executeBenchyProcess(ctx, jobID, displayModelName, servedModelName, tokenizer, baseURL, apiKey, opts)
	pm.finishBenchyJob(jobID, status, exitCode, err)
}

func (pm *ProxyManager) runBenchyQueueJob(
	ctx context.Context,
	queueJobID string,
	models []benchyResolvedModel,
	fixedTokenizer string,
	baseURL string,
	userProvidedBaseURL bool,
	apiKey string,
	opts benchyRunOptions,
	trustRemoteCodeOverride *bool,
) {
	total := len(models)
	if total == 0 {
		pm.finishBenchyJob(queueJobID, benchyStatusError, nil, errors.New("empty benchy queue"))
		return
	}

	for idx, model := range models {
		if errors.Is(ctx.Err(), context.Canceled) {
			pm.finishBenchyJob(queueJobID, benchyStatusCanceled, nil, nil)
			return
		}

		pm.setBenchyQueueProgress(queueJobID, idx, model.RequestedModel, idx)
		pm.appendBenchyOutput(
			queueJobID,
			"stdout",
			fmt.Sprintf("\n[queue] (%d/%d) loading model %s\n", idx+1, total, model.RequestedModel),
		)

		if err := pm.ensureBenchyModelReady(ctx, model.RealModelName); err != nil {
			pm.stopBenchyModel(model.RealModelName)
			if errors.Is(err, context.Canceled) {
				pm.finishBenchyJob(queueJobID, benchyStatusCanceled, nil, nil)
				return
			}
			pm.finishBenchyJob(queueJobID, benchyStatusError, nil, fmt.Errorf("failed to load model %s: %w", model.RequestedModel, err))
			return
		}

		tokenizer := fixedTokenizer
		if tokenizer == "" {
			tokenizer = pm.defaultBenchyTokenizer(model.RealModelName)
		}

		servedModelName := pm.resolveBenchyServedModelName(
			model.RequestedModel,
			model.RealModelName,
			tokenizer,
			userProvidedBaseURL,
		)
		displayModelName := benchyDisplayModelName(servedModelName, tokenizer)
		pm.appendBenchyOutput(
			queueJobID,
			"stdout",
			fmt.Sprintf("[queue] (%d/%d) running benchy for %s\n", idx+1, total, model.RequestedModel),
		)

		runOpts := opts
		runOpts.TrustRemoteCode = pm.resolveBenchyTrustRemoteCode(model.RealModelName, trustRemoteCodeOverride)

		status, exitCode, runErr := pm.executeBenchyProcess(
			ctx,
			queueJobID,
			displayModelName,
			servedModelName,
			tokenizer,
			baseURL,
			apiKey,
			runOpts,
		)

		pm.stopBenchyModel(model.RealModelName)

		if status == benchyStatusCanceled {
			pm.finishBenchyJob(queueJobID, benchyStatusCanceled, exitCode, nil)
			return
		}
		if runErr != nil {
			pm.finishBenchyJob(
				queueJobID,
				benchyStatusError,
				exitCode,
				fmt.Errorf("benchy failed for model %s: %w", model.RequestedModel, runErr),
			)
			return
		}

		pm.appendBenchyOutput(
			queueJobID,
			"stdout",
			fmt.Sprintf("[queue] (%d/%d) completed %s\n", idx+1, total, model.RequestedModel),
		)
		pm.setBenchyQueueProgress(queueJobID, idx+1, "", idx+1)
	}

	pm.finishBenchyJob(queueJobID, benchyStatusDone, intPtr(0), nil)
}

func (pm *ProxyManager) executeBenchyProcess(
	ctx context.Context,
	jobID, displayModelName, servedModelName, tokenizer, baseURL, apiKey string,
	opts benchyRunOptions,
) (BenchyJobStatus, *int, error) {
	benchyCmd, benchyArgs, err := resolveBenchyCommand(opts.EnableIntelligence)
	if err != nil {
		return benchyStatusError, nil, err
	}

	args := buildBenchyArgs(baseURL, displayModelName, servedModelName, tokenizer, apiKey, opts)

	cmd := exec.CommandContext(ctx, benchyCmd, append(benchyArgs, args...)...)
	cmd.Env = os.Environ()
	if opts.EnableIntelligence && containsPlugin(opts.IntelligencePlugins, "swebench_verified") {
		if shimDir := pm.benchyPythonShimDir(); shimDir != "" {
			cmd.Env = prependEnvPathList(cmd.Env, "PYTHONPATH", shimDir)
			cmd.Env = setOrReplaceEnv(cmd.Env, benchyEnvSwebenchShim, "1")
		}
	}
	cmd.Stdout = benchyJobWriter{pm: pm, jobID: jobID, stream: "stdout"}
	cmd.Stderr = benchyJobWriter{pm: pm, jobID: jobID, stream: "stderr"}
	if opts.TrustRemoteCode {
		// llama-benchy currently has no CLI flag to pass `trust_remote_code=True` through to transformers,
		// so we pre-answer its interactive prompt (if it appears). Provide multiple lines in case it's asked more than once.
		cmd.Stdin = strings.NewReader("y\ny\ny\ny\ny\ny\ny\ny\n")
	}

	startErr := cmd.Start()
	if startErr != nil {
		return benchyStatusError, nil, startErr
	}

	waitErr := cmd.Wait()
	if waitErr != nil {
		// Context cancellation should map to canceled even if Wait returns an error.
		if errors.Is(ctx.Err(), context.Canceled) {
			return benchyStatusCanceled, exitCodeFromErr(waitErr), nil
		}
		return benchyStatusError, exitCodeFromErr(waitErr), waitErr
	}

	return benchyStatusDone, intPtr(0), nil
}

type benchyJobWriter struct {
	pm     *ProxyManager
	jobID  string
	stream string // "stdout" | "stderr"
}

func (w benchyJobWriter) Write(p []byte) (int, error) {
	if w.pm == nil || w.jobID == "" {
		return len(p), nil
	}
	w.pm.appendBenchyOutput(w.jobID, w.stream, string(p))
	return len(p), nil
}

func (pm *ProxyManager) appendBenchyOutput(jobID, stream, chunk string) {
	if chunk == "" {
		return
	}

	pm.benchyMu.Lock()
	defer pm.benchyMu.Unlock()

	job := pm.benchyJobs[jobID]
	if job == nil {
		return
	}

	switch stream {
	case "stderr":
		job.Stderr = appendCapped(job.Stderr, chunk, benchyMaxOutputBytes)
	default:
		job.Stdout = appendCapped(job.Stdout, chunk, benchyMaxOutputBytes)
	}
}

func (pm *ProxyManager) finishBenchyJob(jobID string, status BenchyJobStatus, exitCode *int, err error) {
	now := time.Now()

	pm.benchyMu.Lock()
	defer pm.benchyMu.Unlock()

	job := pm.benchyJobs[jobID]
	if job == nil {
		return
	}

	job.Status = status
	job.FinishedAt = &now
	job.ExitCode = exitCode
	if err != nil {
		job.Error = err.Error()
	}

	// Job is complete: release cancel resources and drop cancel function to avoid leaks.
	if cancel, ok := pm.benchyCancels[jobID]; ok && cancel != nil {
		cancel()
	}
	delete(pm.benchyCancels, jobID)

	pm.pruneBenchyJobsLocked()
}

func (pm *ProxyManager) pruneBenchyJobsLocked() {
	if len(pm.benchyJobs) <= benchyMaxJobsInMemory {
		return
	}

	// Remove oldest finished jobs first; never remove running jobs.
	type finished struct {
		id string
		t  time.Time
	}
	finishedJobs := make([]finished, 0, len(pm.benchyJobs))
	for id, job := range pm.benchyJobs {
		if job == nil || job.Status == benchyStatusRunning || job.FinishedAt == nil {
			continue
		}
		finishedJobs = append(finishedJobs, finished{id: id, t: *job.FinishedAt})
	}
	if len(finishedJobs) == 0 {
		return
	}

	sort.Slice(finishedJobs, func(i, j int) bool { return finishedJobs[i].t.Before(finishedJobs[j].t) })
	for len(pm.benchyJobs) > benchyMaxJobsInMemory && len(finishedJobs) > 0 {
		rm := finishedJobs[0]
		finishedJobs = finishedJobs[1:]
		delete(pm.benchyJobs, rm.id)
		delete(pm.benchyCancels, rm.id)
	}
}

func (pm *ProxyManager) setBenchyQueueProgress(jobID string, currentIndex int, currentModel string, completed int) {
	pm.benchyMu.Lock()
	defer pm.benchyMu.Unlock()

	job := pm.benchyJobs[jobID]
	if job == nil {
		return
	}
	if currentIndex >= 0 {
		job.QueueCurrentIndex = currentIndex
	}
	job.QueueCurrentModel = currentModel
	if completed >= 0 {
		job.QueueCompletedCount = completed
	}
}

func normalizeBenchyQueueModels(primary string, queue []string) ([]string, error) {
	out := make([]string, 0, len(queue)+1)
	for _, raw := range queue {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		out = append(out, s)
	}

	s := strings.TrimSpace(primary)
	if s == "" {
		if len(out) == 0 {
			return nil, errors.New("model is required")
		}
		return out, nil
	}
	// Always place primary as the first model; append any additional queue models after it.
	return append([]string{s}, out...), nil
}

func (pm *ProxyManager) ensureBenchyModelReady(ctx context.Context, realModelName string) error {
	processGroup, err := pm.swapProcessGroup(realModelName)
	if err != nil {
		return err
	}
	process, ok := processGroup.GetMember(realModelName)
	if !ok || process == nil {
		return fmt.Errorf("process not found for model %s", realModelName)
	}

	if process.CurrentState() == StateReady {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", "/", nil)
	if err != nil {
		return err
	}
	discardWriter := &DiscardWriter{}
	proxyErr := processGroup.ProxyRequest(realModelName, discardWriter, req)
	if proxyErr != nil && process.CurrentState() != StateReady {
		return proxyErr
	}

	readyDeadline := time.Now().Add(time.Duration(pm.config.HealthCheckTimeout+10) * time.Second)
	for {
		switch process.CurrentState() {
		case StateReady:
			return nil
		case StateShutdown:
			return fmt.Errorf("model %s is shutdown", realModelName)
		case StateStopped:
			if time.Now().After(readyDeadline) {
				return fmt.Errorf("model %s did not reach ready state", realModelName)
			}
		}

		if errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		if time.Now().After(readyDeadline) {
			return fmt.Errorf("timed out waiting for model %s to be ready", realModelName)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (pm *ProxyManager) stopBenchyModel(realModelName string) {
	processGroup := pm.findGroupByModelName(realModelName)
	if processGroup == nil {
		return
	}
	_ = processGroup.StopProcess(realModelName, StopImmediately)
}

func defaultBenchyBaseURLFromRequest(c *gin.Context) string {
	scheme := "http"
	if xf := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")); xf != "" {
		scheme = strings.TrimSpace(strings.Split(xf, ",")[0])
	} else if c.Request.TLS != nil {
		scheme = "https"
	}

	host := strings.TrimSpace(c.Request.Host)
	if xfh := strings.TrimSpace(c.GetHeader("X-Forwarded-Host")); xfh != "" {
		host = strings.TrimSpace(strings.Split(xfh, ",")[0])
	}
	if host == "" {
		host = "localhost"
	}

	return fmt.Sprintf("%s://%s/v1", scheme, host)
}

func normalizeBenchyBaseURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("baseUrl is required")
	}
	s = strings.TrimRight(s, "/")
	if strings.HasSuffix(s, "/v1") {
		return s, nil
	}
	return s + "/v1", nil
}

func (pm *ProxyManager) defaultBenchyTokenizer(realModelName string) string {
	modelCfg, ok := pm.config.Models[realModelName]
	if !ok {
		return realModelName
	}

	if tok, ok := benchyTokenizerFromMetadata(modelCfg.Metadata); ok {
		return tok
	}

	// If the model id itself is a HF-like reference, use it.
	if strings.Contains(realModelName, "/") && !strings.HasPrefix(realModelName, "/") {
		return realModelName
	}

	// If useModelName looks like a HF-like reference, prefer it.
	if u := strings.TrimSpace(modelCfg.UseModelName); u != "" && strings.Contains(u, "/") && !strings.HasPrefix(u, "/") {
		return u
	}

	// Finally, try any alias that looks like a HF model id.
	for _, a := range modelCfg.Aliases {
		a = strings.TrimSpace(a)
		if a != "" && strings.Contains(a, "/") && !strings.HasPrefix(a, "/") {
			return a
		}
	}

	return realModelName
}

func (pm *ProxyManager) resolveBenchyServedModelName(requestedModel, realModelName, tokenizer string, userProvidedBaseURL bool) string {
	// For llama-swap base URL (default), use requestedModel so normal swap routing resolves correctly.
	if !userProvidedBaseURL {
		return requestedModel
	}

	// For direct upstreams (explicit base URL), this should match that upstream's expected model id.
	servedModelName := requestedModel
	if cfg, ok := pm.config.Models[realModelName]; ok {
		if u := strings.TrimSpace(cfg.UseModelName); u != "" {
			servedModelName = u
		} else if tokenizer != "" && strings.Contains(tokenizer, "/") && !strings.HasPrefix(tokenizer, "/") {
			servedModelName = tokenizer
		} else {
			// Fall back to a HF-ish alias if available.
			for _, a := range cfg.Aliases {
				a = strings.TrimSpace(a)
				if a != "" && strings.Contains(a, "/") && !strings.HasPrefix(a, "/") {
					servedModelName = a
					break
				}
			}
		}
	}
	return servedModelName
}

func (pm *ProxyManager) resolveBenchyTrustRemoteCode(realModelName string, override *bool) bool {
	if override != nil {
		return *override
	}
	if cfg, ok := pm.config.Models[realModelName]; ok {
		if v, ok := benchyTrustRemoteCodeFromMetadata(cfg.Metadata); ok {
			return v
		}
	}
	return false
}

func benchyDisplayModelName(servedModelName, tokenizer string) string {
	// Prefer a human-friendly HF-ish model id for display if we have it as tokenizer.
	if tokenizer != "" && strings.Contains(tokenizer, "/") && !strings.HasPrefix(tokenizer, "/") {
		return tokenizer
	}
	return servedModelName
}

func benchyTokenizerFromMetadata(meta map[string]any) (string, bool) {
	if len(meta) == 0 {
		return "", false
	}

	// Accept a few common shapes:
	// metadata:
	//   tokenizer: openai/gpt-oss-120b
	// or
	// metadata:
	//   benchy_tokenizer: openai/gpt-oss-120b
	// or
	// metadata:
	//   benchy:
	//     tokenizer: openai/gpt-oss-120b
	if v, ok := meta["benchy_tokenizer"]; ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s), true
		}
	}
	if v, ok := meta["tokenizer"]; ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s), true
		}
	}
	if v, ok := meta["benchy"]; ok {
		if m, ok := v.(map[string]any); ok {
			if tv, ok := m["tokenizer"]; ok {
				if s, ok := tv.(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s), true
				}
			}
		}
	}

	return "", false
}

func benchyTrustRemoteCodeFromMetadata(meta map[string]any) (bool, bool) {
	if len(meta) == 0 {
		return false, false
	}

	// Accept a few common shapes:
	// metadata:
	//   benchy_trust_remote_code: true
	// or
	// metadata:
	//   benchy:
	//     trust_remote_code: true
	// or (less preferred, but convenient)
	// metadata:
	//   trust_remote_code: true
	if v, ok := meta["benchy_trust_remote_code"]; ok {
		return parseAnyBool(v)
	}
	if v, ok := meta["trust_remote_code"]; ok {
		return parseAnyBool(v)
	}
	if v, ok := meta["benchy"]; ok {
		if m, ok := v.(map[string]any); ok {
			if tv, ok := m["trust_remote_code"]; ok {
				return parseAnyBool(tv)
			}
			if tv, ok := m["trustRemoteCode"]; ok {
				return parseAnyBool(tv)
			}
		}
	}

	return false, false
}

func parseAnyBool(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return false, false
		}
		b, err := strconv.ParseBool(s)
		if err != nil {
			return false, false
		}
		return b, true
	case int:
		return t != 0, true
	case int64:
		return t != 0, true
	case float64:
		return t != 0, true
	default:
		return false, false
	}
}

func validatePositiveIntSlice(field string, values []int) error {
	for i, v := range values {
		if v <= 0 {
			return fmt.Errorf("%s[%d] must be > 0", field, i)
		}
	}
	return nil
}

func validateNonNegativeIntSlice(field string, values []int) error {
	for i, v := range values {
		if v < 0 {
			return fmt.Errorf("%s[%d] must be >= 0", field, i)
		}
	}
	return nil
}

func normalizeBenchyLatencyMode(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "", "api", "generation", "none":
		return v, nil
	default:
		return "", errors.New("latencyMode must be one of: api, generation, none")
	}
}

func buildBenchyArgs(baseURL, displayModelName, servedModelName, tokenizer, apiKey string, opts benchyRunOptions) []string {
	args := []string{
		"--base-url", baseURL,
		"--model", displayModelName,
		"--served-model-name", servedModelName,
		"--tokenizer", tokenizer,
		"--runs", strconv.Itoa(opts.Runs),
	}
	if apiKey != "" {
		args = append(args, "--api-key", apiKey)
	}

	args = append(args, "--pp")
	for _, n := range opts.PP {
		args = append(args, strconv.Itoa(n))
	}

	args = append(args, "--tg")
	for _, n := range opts.TG {
		args = append(args, strconv.Itoa(n))
	}

	if len(opts.Depth) > 0 {
		args = append(args, "--depth")
		for _, n := range opts.Depth {
			args = append(args, strconv.Itoa(n))
		}
	}

	if len(opts.Concurrency) > 0 {
		args = append(args, "--concurrency")
		for _, n := range opts.Concurrency {
			args = append(args, strconv.Itoa(n))
		}
	}

	if opts.LatencyMode != "" {
		args = append(args, "--latency-mode", opts.LatencyMode)
	}
	if opts.NoCache {
		args = append(args, "--no-cache")
	}
	if opts.NoWarmup {
		args = append(args, "--no-warmup")
	}
	if opts.AdaptPrompt != nil {
		if *opts.AdaptPrompt {
			args = append(args, "--adapt-prompt")
		} else {
			args = append(args, "--no-adapt-prompt")
		}
	}
	if opts.EnablePrefixCaching {
		args = append(args, "--enable-prefix-caching")
	}
	if opts.EnableIntelligence {
		args = append(args, "--enable-intelligence")
		if opts.OutputDir != "" {
			args = append(args, "--output-dir", opts.OutputDir)
		}
		if len(opts.IntelligencePlugins) > 0 {
			args = append(args, "--intelligence-plugins")
			args = append(args, opts.IntelligencePlugins...)
		}
		if opts.AllowCodeExec {
			args = append(args, "--allow-code-exec")
		}
		if opts.DatasetCacheDir != "" {
			args = append(args, "--dataset-cache-dir", opts.DatasetCacheDir)
		}
		if opts.MaxConcurrent != nil {
			args = append(args, "--max-concurrent", strconv.Itoa(*opts.MaxConcurrent))
		}
	}

	return args
}

func resolveBenchyCommand(enableIntelligence bool) (string, []string, error) {
	if raw := strings.TrimSpace(os.Getenv(benchyEnvCmd)); raw != "" {
		parts := strings.Fields(raw)
		if len(parts) == 0 {
			return "", nil, errors.New("LLAMA_BENCHY_CMD is empty")
		}
		return parts[0], parts[1:], nil
	}

	if path, err := exec.LookPath("uvx"); err == nil && path != "" {
		if enableIntelligence {
			return path, []string{"--from", benchyForkIntelligenceReqRef, "llama-benchy"}, nil
		}
		return path, []string{"--from", benchyForkGitRef, "llama-benchy"}, nil
	}

	// Common in systemd services: ~/.local/bin isn't on PATH.
	// If uvx is installed for the service user, try there too.
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		if path, err := exec.LookPath(filepath.Join(home, ".local", "bin", "uvx")); err == nil && path != "" {
			if enableIntelligence {
				return path, []string{"--from", benchyForkIntelligenceReqRef, "llama-benchy"}, nil
			}
			return path, []string{"--from", benchyForkGitRef, "llama-benchy"}, nil
		}
	}

	if path, err := exec.LookPath("llama-benchy"); err == nil && path != "" {
		return path, nil, nil
	}

	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		if path, err := exec.LookPath(filepath.Join(home, ".local", "bin", "llama-benchy")); err == nil && path != "" {
			return path, nil, nil
		}
	}

	return "", nil, errors.New("unable to find llama-benchy runner (install uvx or llama-benchy, or set LLAMA_BENCHY_CMD)")
}

func normalizeIntelligencePlugins(plugins []string) ([]string, error) {
	out := make([]string, 0, len(plugins))
	seen := make(map[string]struct{}, len(plugins))
	for _, raw := range plugins {
		p := strings.ToLower(strings.TrimSpace(raw))
		if p == "" {
			continue
		}
		if _, ok := benchyIntelligencePluginChoices[p]; !ok {
			return nil, fmt.Errorf("unknown intelligence plugin: %s", p)
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out, nil
}

func firstPluginThatRequiresCodeExec(plugins []string) (string, bool) {
	for _, p := range plugins {
		if _, ok := benchyPluginsRequireCodeExec[p]; ok {
			return p, true
		}
	}
	return "", false
}

func defaultBenchyOutputDir() string {
	if v := strings.TrimSpace(os.Getenv(benchyEnvOutputDir)); v != "" {
		return v
	}
	return benchyDefaultOutputDir
}

func (pm *ProxyManager) benchyPythonShimDir() string {
	if v := strings.TrimSpace(os.Getenv(benchyEnvPyShimDir)); v != "" {
		if hasSiteCustomizeFile(v) {
			return v
		}
		return ""
	}
	if pm == nil || strings.TrimSpace(pm.configPath) == "" {
		return ""
	}
	cfgDir := filepath.Dir(pm.configPath)
	candidate := filepath.Join(cfgDir, "proxy", "pyshim")
	if hasSiteCustomizeFile(candidate) {
		return candidate
	}
	return ""
}

func hasSiteCustomizeFile(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "sitecustomize.py"))
	return err == nil && !info.IsDir()
}

func containsPlugin(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func prependEnvPathList(env []string, key, value string) []string {
	if key == "" || value == "" {
		return env
	}
	sep := string(os.PathListSeparator)
	prefix := key + "="
	for i, item := range env {
		if !strings.HasPrefix(item, prefix) {
			continue
		}
		current := strings.TrimPrefix(item, prefix)
		if current == "" {
			env[i] = prefix + value
			return env
		}
		for _, part := range strings.Split(current, sep) {
			if part == value {
				return env
			}
		}
		env[i] = prefix + value + sep + current
		return env
	}
	return append(env, prefix+value)
}

func setOrReplaceEnv(env []string, key, value string) []string {
	if key == "" {
		return env
	}
	prefix := key + "="
	for i, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func expandLeadingTilde(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~"+string(os.PathSeparator)) {
		home, err := os.UserHomeDir()
		if err == nil && strings.TrimSpace(home) != "" {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func newBenchyJobID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func exitCodeFromErr(err error) *int {
	var exitErr *exec.ExitError
	if err == nil || !errors.As(err, &exitErr) {
		return nil
	}
	return intPtr(exitErr.ExitCode())
}

func intPtr(v int) *int {
	return &v
}

func appendCapped(prev, chunk string, maxBytes int) string {
	if chunk == "" {
		return prev
	}
	if maxBytes <= 0 {
		return prev + chunk
	}
	if len(chunk) >= maxBytes {
		return chunk[len(chunk)-maxBytes:]
	}

	combined := prev + chunk
	if len(combined) > maxBytes {
		return combined[len(combined)-maxBytes:]
	}
	return combined
}
