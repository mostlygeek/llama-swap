package mantle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"

	"github.com/billziss-gh/golib/shlex"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/shared"
)

// Handler bundles all mantle API handlers with their dependencies.
type Handler struct {
	tm          *TaskManager
	cfg         *config.Config
	configPath  string
	modelsDir   string
	backendsDir string
	buildScript string
}

// NewHandler creates a new mantle API handler.
func NewHandler(cfg *config.Config, configPath, modelsDir, backendsDir, buildScript string) *Handler {
	return &Handler{
		tm:          NewTaskManager(),
		cfg:         cfg,
		configPath:  configPath,
		modelsDir:   modelsDir,
		backendsDir: backendsDir,
		buildScript: buildScript,
	}
}

// RegisterRoutes adds all mantle API endpoints to the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// HF model browsing
	mux.HandleFunc("GET /api/mantle/models/search", h.handleSearchModels)
	mux.HandleFunc("GET /api/mantle/models/files", h.handleListModelFiles)

	// Download management
	mux.HandleFunc("POST /api/mantle/models/download", h.handleStartDownload)
	mux.HandleFunc("DELETE /api/mantle/models/download/{id}", h.handleCancelDownload)
	mux.HandleFunc("GET /api/mantle/models/download/{id}/stream", h.handleDownloadProgress)

	// Local model management
	mux.HandleFunc("GET /api/mantle/models/local", h.handleListLocalModels)
	mux.HandleFunc("DELETE /api/mantle/models/local/{name...}", h.handleDeleteLocalModel)

	// Config management
	mux.HandleFunc("GET /api/mantle/config", h.handleGetConfig)
	mux.HandleFunc("PUT /api/mantle/config", h.handlePutConfig)

	// Backend builds
	mux.HandleFunc("POST /api/mantle/backends/build", h.handleStartBuild)
	mux.HandleFunc("DELETE /api/mantle/backends/build/{id}", h.handleCancelBuild)
	mux.HandleFunc("GET /api/mantle/backends/build/{id}/stream", h.handleBuildProgress)
	mux.HandleFunc("GET /api/mantle/backends", h.handleListBackends)
	mux.HandleFunc("DELETE /api/mantle/backends/{name...}", h.handleDeleteBackend)

	// Task status
	mux.HandleFunc("GET /api/mantle/tasks", h.handleListTasks)
	mux.HandleFunc("GET /api/mantle/tasks/{id}", h.handleGetTask)
}

func jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	jsonResponse(w, status, map[string]string{"error": msg})
}

// --- HF Model Search ---

func (h *Handler) handleSearchModels(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		jsonError(w, http.StatusBadRequest, "query parameter 'q' is required")
		return
	}
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}
	models, err := SearchHFModels(query, limit)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, models)
}

func (h *Handler) handleListModelFiles(w http.ResponseWriter, r *http.Request) {
	modelID := r.URL.Query().Get("model")
	if modelID == "" {
		jsonError(w, http.StatusBadRequest, "query parameter 'model' is required")
		return
	}
	files, err := ListHFFiles(modelID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, files)
}

// --- Download ---

type downloadRequest struct {
	ModelID  string `json:"modelID"`
	Filename string `json:"filename"`
}

func (h *Handler) handleStartDownload(w http.ResponseWriter, r *http.Request) {
	var req downloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.ModelID == "" || req.Filename == "" {
		jsonError(w, http.StatusBadRequest, "modelID and filename are required")
		return
	}
	task := h.tm.StartDownload(req.ModelID, req.Filename, h.modelsDir)
	jsonResponse(w, http.StatusAccepted, task)
}

func (h *Handler) handleCancelDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !h.tm.CancelTask(id) {
		jsonError(w, http.StatusNotFound, "task not found or already completed")
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"msg": "cancelled"})
}

func (h *Handler) handleDownloadProgress(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task := h.tm.GetTask(id)
	if task == nil {
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}
	h.streamProgress(w, r, task)
}

// --- Local Models ---

func (h *Handler) handleListLocalModels(w http.ResponseWriter, r *http.Request) {
	models, err := ListLocalModels(h.modelsDir)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, models)
}

func (h *Handler) handleDeleteLocalModel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		jsonError(w, http.StatusBadRequest, "model name is required")
		return
	}
	if err := DeleteLocalModel(h.modelsDir, name); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"msg": "deleted"})
}

// --- Config ---

func (h *Handler) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(h.configPath)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("failed to read config: %v", err))
		return
	}
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Write(data)
}

func (h *Handler) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "failed to read body")
		return
	}

	// Validate by attempting to parse
	newCfg, err := config.LoadConfigFromReader(bytes.NewReader(body))
	if err != nil {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("invalid config: %v", err))
		return
	}

	// Preserve runtime paths
	newCfg.ConfigPath = h.configPath
	newCfg.ModelsDir = h.modelsDir
	newCfg.BackendsDir = h.backendsDir
	newCfg.BuildScript = h.buildScript

	// Write to config file
	if err := os.WriteFile(h.configPath, body, 0644); err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("failed to write config: %v", err))
		return
	}

	// Emit config changed event so the server hot-reloads
	event.Emit(shared.ConfigFileChangedEvent{State: shared.ReloadingStateStart})
	*h.cfg = newCfg
	event.Emit(shared.ConfigFileChangedEvent{State: shared.ReloadingStateEnd})

	jsonResponse(w, http.StatusOK, map[string]string{"msg": "config updated and reloaded"})
}

// --- Backend Builds ---

type buildRequest struct {
	BackendName string   `json:"backendName"`
	Repo        string   `json:"repo"`
	Branch      string   `json:"branch"`
	CMakeArgs   []string `json:"cmakeArgs"`
	CMakeFlags  string   `json:"cmakeFlags"`
}

func (h *Handler) handleStartBuild(w http.ResponseWriter, r *http.Request) {
	var req buildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Repo == "" {
		jsonError(w, http.StatusBadRequest, "repo is required")
		return
	}
	if req.BackendName != "" && !isSafeBackendName(req.BackendName) {
		jsonError(w, http.StatusBadRequest, "backendName may only contain letters, numbers, dot, underscore, and hyphen")
		return
	}
	cmakeArgs := req.CMakeArgs
	if req.CMakeFlags != "" {
		var parsed []string
		if runtime.GOOS == "windows" {
			parsed = shlex.Windows.Split(req.CMakeFlags)
		} else {
			parsed = shlex.Posix.Split(req.CMakeFlags)
		}
		if parsed == nil {
			jsonError(w, http.StatusBadRequest, "invalid CMake flags")
			return
		}
		cmakeArgs = append(cmakeArgs, parsed...)
	}
	task := h.tm.StartBuild(req.Repo, req.Branch, req.BackendName, h.buildScript, h.backendsDir, cmakeArgs)
	jsonResponse(w, http.StatusAccepted, task)
}

func (h *Handler) handleCancelBuild(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !h.tm.CancelTask(id) {
		jsonError(w, http.StatusNotFound, "task not found or already completed")
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"msg": "cancelled"})
}

func (h *Handler) handleBuildProgress(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task := h.tm.GetTask(id)
	if task == nil {
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}
	h.streamProgress(w, r, task)
}

// --- Backend Listing ---

func (h *Handler) handleListBackends(w http.ResponseWriter, r *http.Request) {
	backends, err := ListBackends(h.backendsDir)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, backends)
}

func (h *Handler) handleDeleteBackend(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		jsonError(w, http.StatusBadRequest, "backend name is required")
		return
	}
	if err := DeleteBackend(h.backendsDir, name); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"msg": "deleted"})
}

// --- Task listing ---

func (h *Handler) handleListTasks(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, h.tm.ListTasks())
}

func (h *Handler) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task := h.tm.GetTask(id)
	if task == nil {
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}
	jsonResponse(w, http.StatusOK, task)
}

// streamProgress sends SSE events for a task's progress.
// It subscribes to the appropriate progress events and forwards them.
func (h *Handler) streamProgress(w http.ResponseWriter, r *http.Request, task *Task) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	// Send initial state
	task.mu.Lock()
	initial := map[string]any{
		"id":      task.ID,
		"type":    task.Type,
		"state":   task.State,
		"message": task.Message,
		"pct":     task.Pct,
	}
	task.mu.Unlock()
	data, _ := json.Marshal(initial)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	// Subscribe to events
	ctx := r.Context()
	eventCh := make(chan any, 64)

	var cancel1, cancel2 context.CancelFunc

	if task.Type == "build" {
		cancel1 = event.SubscribeTo(event.Default, shared.BackendBuildProgressEventID,
			func(ev shared.BackendBuildProgressEvent) {
				select {
				case eventCh <- ev:
				case <-ctx.Done():
				default:
				}
			})
	} else if task.Type == "download" {
		cancel2 = event.SubscribeTo(event.Default, shared.ModelDownloadProgressEventID,
			func(ev shared.ModelDownloadProgressEvent) {
				select {
				case eventCh <- ev:
				case <-ctx.Done():
				default:
				}
			})
	}

	defer func() {
		if cancel1 != nil {
			cancel1()
		}
		if cancel2 != nil {
			cancel2()
		}
	}()

	// Also subscribe to generic task state changes for task completion
	taskDone := task.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-taskDone:
			// Final state
			task.mu.Lock()
			final := map[string]any{
				"id":      task.ID,
				"state":   task.State,
				"message": task.Message,
				"pct":     task.Pct,
			}
			task.mu.Unlock()
			data, _ := json.Marshal(final)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			return
		case ev := <-eventCh:
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
