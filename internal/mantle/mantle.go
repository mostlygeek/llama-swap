package mantle

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/shared"
)

// TaskState is the current state of a long-running task.
type TaskState string

const (
	TaskRunning   TaskState = "running"
	TaskCompleted TaskState = "completed"
	TaskFailed    TaskState = "failed"
	TaskCancelled TaskState = "cancelled"
)

// Task tracks a long-running operation (download or build).
type Task struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"` // "download" or "build"
	State     TaskState `json:"state"`
	Message   string    `json:"message"`
	Pct       int       `json:"pct"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	// Type-specific metadata
	Repo    string `json:"repo,omitempty"`
	Branch  string `json:"branch,omitempty"`
	ModelID string `json:"modelID,omitempty"`

	cancel   context.CancelFunc
	cancelCh chan struct{}
	mu       sync.Mutex
}

// Done returns a channel that's closed when the task is cancelled.
func (t *Task) Done() <-chan struct{} {
	return t.cancelCh
}

// Cancel cancels the task.
func (t *Task) Cancel() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.State != TaskRunning {
		return
	}
	if t.cancel != nil {
		t.cancel()
	}
	close(t.cancelCh)
	t.State = TaskCancelled
	t.UpdatedAt = time.Now()
}

// UpdateProgress updates a task's state and emits a progress event.
func (t *Task) UpdateProgress(state TaskState, msg string, pct int) {
	t.mu.Lock()
	t.State = state
	t.Message = msg
	t.Pct = pct
	t.UpdatedAt = time.Now()
	t.mu.Unlock()

	if t.Type == "build" {
		event.Emit(shared.BackendBuildProgressEvent{
			TaskID:  t.ID,
			Repo:    t.Repo,
			Branch:  t.Branch,
			State:   shared.ProgressState(state),
			Message: msg,
			Pct:     pct,
		})
	} else if t.Type == "download" {
		event.Emit(shared.ModelDownloadProgressEvent{
			TaskID:  t.ID,
			ModelID: t.ModelID,
			State:   shared.ProgressState(state),
			Message: msg,
			Pct:     pct,
		})
	}
}

// TaskManager holds all active and recent tasks.
type TaskManager struct {
	mu    sync.Mutex
	tasks map[string]*Task
	next  int
}

// NewTaskManager creates a new task manager.
func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasks: make(map[string]*Task),
	}
}

func (tm *TaskManager) newID() string {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.next++
	return fmt.Sprintf("task-%d", tm.next)
}

// CreateTask registers a new task with a cancellable context and returns it.
func (tm *TaskManager) CreateTask(taskType, repo, branch, modelID string) *Task {
	ctx, cancel := context.WithCancel(context.Background())
	t := &Task{
		ID:        tm.newID(),
		Type:      taskType,
		State:     TaskRunning,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Repo:      repo,
		Branch:    branch,
		ModelID:   modelID,
		cancel:    cancel,
		cancelCh:  make(chan struct{}),
	}
	_ = ctx // context is used via cancel()

	tm.mu.Lock()
	tm.tasks[t.ID] = t
	tm.mu.Unlock()
	return t
}

// GetTask returns a task by ID.
func (tm *TaskManager) GetTask(id string) *Task {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.tasks[id]
}

// ListTasks returns all recent tasks.
func (tm *TaskManager) ListTasks() []*Task {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	result := make([]*Task, 0, len(tm.tasks))
	for _, t := range tm.tasks {
		result = append(result, t)
	}
	return result
}

// CancelTask cancels a running task by ID.
func (tm *TaskManager) CancelTask(id string) bool {
	t := tm.GetTask(id)
	if t == nil {
		return false
	}
	t.Cancel()
	return true
}

// ---------------------------------------------------------------------------
// HF Model search
// ---------------------------------------------------------------------------

// HFModel is a single result from the HuggingFace model API.
type HFModel struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Downloads   int64    `json:"downloads"`
	Likes       int64    `json:"likes"`
	UpdatedAt   string   `json:"updatedAt"`
	Tags        []string `json:"tags"`
	GGUF        bool     `json:"gguf"`
}

// hfSortParam maps a UI sort key to the HuggingFace API sort field. An empty
// field means "relevance" (let the search ranking decide the order).
func hfSortParam(sort string) string {
	switch sort {
	case "relevance":
		return ""
	case "trending":
		return "trendingScore"
	case "likes":
		return "likes"
	case "created":
		return "createdAt"
	case "modified":
		return "lastModified"
	case "downloads":
		return "downloads"
	default:
		return "downloads"
	}
}

// hfPipelineTag maps a UI model-type "kind" to the HuggingFace pipeline_tag
// filter. An empty result means no pipeline filter (text/LLM models).
func hfPipelineTag(kind string) string {
	switch kind {
	case "image":
		return "text-to-image"
	case "transcription":
		return "automatic-speech-recognition"
	case "tts":
		return "text-to-speech"
	default:
		return ""
	}
}

// SearchHFModels queries the HuggingFace model hub.
// sort is one of: relevance, trending, downloads, likes, created, modified.
// kind filters by model type: "" / "text" (LLMs), "image", "transcription", "tts".
func SearchHFModels(query string, limit int, sort, kind string) ([]HFModel, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	params := url.Values{}
	params.Set("search", query)
	params.Set("limit", fmt.Sprintf("%d", limit))
	if field := hfSortParam(sort); field != "" {
		params.Set("sort", field)
		params.Set("direction", "-1")
	}
	if tag := hfPipelineTag(kind); tag != "" {
		params.Set("pipeline_tag", tag)
	}
	apiURL := "https://huggingface.co/api/models?" + params.Encode()
	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("HF API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HF API returned status %d", resp.StatusCode)
	}

	var raw []struct {
		ID          string   `json:"id"`
		Downloads   int64    `json:"downloads"`
		Likes       int64    `json:"likes"`
		LastUpdated string   `json:"lastModified"`
		Tags        []string `json:"tags"`
		Siblings    []struct {
			Rfilename string `json:"rfilename"`
		} `json:"siblings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("HF API decode failed: %w", err)
	}

	results := make([]HFModel, 0, len(raw))
	for _, m := range raw {
		hasGGUF := false
		for _, s := range m.Siblings {
			if len(s.Rfilename) > 5 && s.Rfilename[len(s.Rfilename)-5:] == ".gguf" {
				hasGGUF = true
				break
			}
		}
		results = append(results, HFModel{
			ID:        m.ID,
			Name:      m.ID,
			Downloads: m.Downloads,
			Likes:     m.Likes,
			UpdatedAt: m.LastUpdated,
			Tags:      m.Tags,
			GGUF:      hasGGUF,
		})
	}
	return results, nil
}

// HFFile is a single downloadable file in a HF model repo.
type HFFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// ListHFFiles lists every file in a HF model repo with its size, using the
// tree API (the basic models endpoint does not report file sizes). LFS-backed
// files (model weights) report their real size under lfs.size.
func ListHFFiles(modelID string) ([]HFFile, error) {
	url := fmt.Sprintf("https://huggingface.co/api/models/%s/tree/main?recursive=true", modelID)
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HF API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HF API returned status %d for model %s", resp.StatusCode, modelID)
	}

	var raw []struct {
		Type string `json:"type"`
		Path string `json:"path"`
		Size int64  `json:"size"`
		LFS  *struct {
			Size int64 `json:"size"`
		} `json:"lfs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("HF API decode failed: %w", err)
	}

	files := make([]HFFile, 0, len(raw))
	for _, e := range raw {
		if e.Type == "directory" {
			continue
		}
		size := e.Size
		if e.LFS != nil && e.LFS.Size > 0 {
			size = e.LFS.Size
		}
		files = append(files, HFFile{Path: e.Path, Size: size})
	}
	return files, nil
}
