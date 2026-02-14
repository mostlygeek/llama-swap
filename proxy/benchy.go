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
)

type BenchyJob struct {
	ID        string `json:"id"`
	Model     string `json:"model"`
	Tokenizer string `json:"tokenizer"`
	BaseURL   string `json:"baseUrl"`
	PP        []int  `json:"pp"`
	TG        []int  `json:"tg"`
	Runs      int    `json:"runs"`

	Status     BenchyJobStatus `json:"status"`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
	ExitCode   *int            `json:"exitCode,omitempty"`

	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	Error  string `json:"error,omitempty"`
}

type benchyStartRequest struct {
	Model     string `json:"model"`
	Tokenizer string `json:"tokenizer,omitempty"`
	BaseURL   string `json:"baseUrl,omitempty"`
	PP        []int  `json:"pp,omitempty"`
	TG        []int  `json:"tg,omitempty"`
	Runs      int    `json:"runs,omitempty"`
}

type benchyStartResponse struct {
	ID string `json:"id"`
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

	requestedModel := strings.TrimSpace(req.Model)
	if requestedModel == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model is required"})
		return
	}

	realModelName, found := pm.config.RealModelName(requestedModel)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "model not found"})
		return
	}

	baseURLRaw := strings.TrimSpace(req.BaseURL)
	if baseURLRaw == "" {
		baseURLRaw = pm.defaultBenchyBaseURLFromModel(realModelName, c)
	}
	baseURL, err := normalizeBenchyBaseURL(baseURLRaw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	pp := req.PP
	if len(pp) == 0 {
		pp = benchyDefaultPP
	}
	tg := req.TG
	if len(tg) == 0 {
		tg = benchyDefaultTG
	}
	runs := req.Runs
	if runs <= 0 {
		runs = benchyDefaultRuns
	}

	// Choose tokenizer: request override > metadata > heuristics
	tokenizer := strings.TrimSpace(req.Tokenizer)
	if tokenizer == "" {
		tokenizer = pm.defaultBenchyTokenizer(realModelName)
	}

	// Choose served model name for the target base URL.
	// For direct upstreams (e.g. vLLM), this must match the model id that /v1 expects.
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

	job := &BenchyJob{
		ID:        jobID,
		Model:     requestedModel,
		Tokenizer: tokenizer,
		BaseURL:   baseURL,
		PP:        append([]int{}, pp...),
		TG:        append([]int{}, tg...),
		Runs:      runs,
		Status:    benchyStatusRunning,
		StartedAt: time.Now(),
	}

	ctx, cancel := context.WithCancel(pm.shutdownCtx)
	pm.benchyMu.Lock()
	pm.benchyJobs[jobID] = job
	pm.benchyCancels[jobID] = cancel
	pm.benchyMu.Unlock()

	go pm.runBenchyJob(ctx, jobID, servedModelName, tokenizer, baseURL, pp, tg, runs, apiKey)

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

func (pm *ProxyManager) runBenchyJob(ctx context.Context, jobID, servedModelName, tokenizer, baseURL string, pp, tg []int, runs int, apiKey string) {
	displayModelName := servedModelName
	// Prefer a human-friendly HF-ish model id for display if we have it as tokenizer.
	if tokenizer != "" && strings.Contains(tokenizer, "/") && !strings.HasPrefix(tokenizer, "/") {
		displayModelName = tokenizer
	}

	benchyCmd, benchyArgs, err := resolveBenchyCommand()
	if err != nil {
		pm.finishBenchyJob(jobID, benchyStatusError, nil, err)
		return
	}

	args := []string{
		"--base-url", baseURL,
		"--model", displayModelName,
		"--served-model-name", servedModelName,
		"--tokenizer", tokenizer,
		"--runs", strconv.Itoa(runs),
	}
	if apiKey != "" {
		args = append(args, "--api-key", apiKey)
	}
	args = append(args, "--pp")
	for _, n := range pp {
		args = append(args, strconv.Itoa(n))
	}
	args = append(args, "--tg")
	for _, n := range tg {
		args = append(args, strconv.Itoa(n))
	}

	cmd := exec.CommandContext(ctx, benchyCmd, append(benchyArgs, args...)...)
	cmd.Stdout = benchyJobWriter{pm: pm, jobID: jobID, stream: "stdout"}
	cmd.Stderr = benchyJobWriter{pm: pm, jobID: jobID, stream: "stderr"}

	startErr := cmd.Start()
	if startErr != nil {
		pm.finishBenchyJob(jobID, benchyStatusError, nil, startErr)
		return
	}

	waitErr := cmd.Wait()
	if waitErr != nil {
		// Context cancellation should map to canceled even if Wait returns an error.
		if errors.Is(ctx.Err(), context.Canceled) {
			pm.finishBenchyJob(jobID, benchyStatusCanceled, exitCodeFromErr(waitErr), nil)
			return
		}
		pm.finishBenchyJob(jobID, benchyStatusError, exitCodeFromErr(waitErr), waitErr)
		return
	}

	pm.finishBenchyJob(jobID, benchyStatusDone, intPtr(0), nil)
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

func (pm *ProxyManager) defaultBenchyBaseURLFromModel(realModelName string, c *gin.Context) string {
	if cfg, ok := pm.config.Models[realModelName]; ok {
		if p := strings.TrimSpace(cfg.Proxy); p != "" {
			p = strings.TrimRight(p, "/")
			if strings.HasSuffix(p, "/v1") {
				return p
			}
			return p + "/v1"
		}
	}
	return defaultBenchyBaseURLFromRequest(c)
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

func resolveBenchyCommand() (string, []string, error) {
	if raw := strings.TrimSpace(os.Getenv(benchyEnvCmd)); raw != "" {
		parts := strings.Fields(raw)
		if len(parts) == 0 {
			return "", nil, errors.New("LLAMA_BENCHY_CMD is empty")
		}
		return parts[0], parts[1:], nil
	}

	if path, err := exec.LookPath("llama-benchy"); err == nil && path != "" {
		return path, nil, nil
	}

	// Common in systemd services: ~/.local/bin isn't on PATH.
	// If llama-benchy is installed for the service user, try there too.
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		if path, err := exec.LookPath(filepath.Join(home, ".local", "bin", "llama-benchy")); err == nil && path != "" {
			return path, nil, nil
		}
	}

	if path, err := exec.LookPath("uvx"); err == nil && path != "" {
		// Uses PyPI; first run may download packages.
		return path, []string{"llama-benchy"}, nil
	}

	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		if path, err := exec.LookPath(filepath.Join(home, ".local", "bin", "uvx")); err == nil && path != "" {
			// Uses PyPI; first run may download packages.
			return path, []string{"llama-benchy"}, nil
		}
	}

	return "", nil, errors.New("unable to find llama-benchy runner (install llama-benchy or uvx, or set LLAMA_BENCHY_CMD)")
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
