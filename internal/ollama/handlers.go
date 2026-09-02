package ollama

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ModelInfo is the slice of model metadata the ollama package needs to render
// /api/tags, /api/show, and /api/ps. It carries only what's required; the
// authoritative source is config.ModelConfig.
type ModelInfo struct {
	ID          string
	Aliases     []string
	Name        string
	Description string
	Metadata    map[string]any

	// PassthroughOllama forwards /api/* requests to the upstream unchanged
	// instead of translating them to OpenAI (the backend natively speaks Ollama).
	PassthroughOllama bool
}

// Dispatcher is the interface the server implements to satisfy the ollama
// handlers. Decoupling avoids an import cycle and keeps this package testable
// in isolation.
type Dispatcher interface {
	// DispatchJSON forwards the (translated, OpenAI-shaped) body to the resolved
	// model's process group, writing the upstream response to w. Errors are
	// written to w via the server's standard error responder.
	DispatchJSON(w http.ResponseWriter, r *http.Request, body []byte)

	// ListModels returns every non-unlisted model in the config, plus aliases
	// when the operator has enabled IncludeAliasesInList.
	ListModels() []ModelInfo

	// FindModel resolves a name (or alias) to its info.
	FindModel(name string) (ModelInfo, bool)

	// RunningModels returns the IDs of currently-loaded models (those in
	// StateReady), used to render /api/ps.
	RunningModels() []string
}

// Options controls route registration.
type Options struct {
	// Version is returned from /api/version when SkipVersion is false. Some
	// Ollama clients gate features on this string; pick a value at or above
	// the current upstream Ollama release.
	Version string

	// SkipVersion omits /api/version from registration. Set this when the
	// caller already serves a compatible /api/version handler (llama-swap's
	// native /api/version returns {"version":...} so it satisfies Ollama
	// clients out of the box).
	SkipVersion bool
}

// Register wires the Ollama-compatible endpoints. reg mounts each handler with
// the caller's middleware (the server passes auth + in-flight tracking).
func Register(reg func(method, path string, h http.HandlerFunc), d Dispatcher, opts Options) {
	if opts.Version == "" {
		opts.Version = "0.0.0-llama-swap"
	}

	// Inference endpoints — translated to OpenAI shape, forwarded.
	reg(http.MethodPost, "/api/chat", makeChatHandler(d))
	reg(http.MethodPost, "/api/generate", makeGenerateHandler(d))
	reg(http.MethodPost, "/api/embed", makeEmbedHandler(d))
	reg(http.MethodPost, "/api/embeddings", makeEmbeddingsHandler(d))

	// Informational endpoints — answered locally.
	reg(http.MethodGet, "/api/tags", makeTagsHandler(d))
	reg(http.MethodPost, "/api/show", makeShowHandler(d))
	reg(http.MethodGet, "/api/ps", makePsHandler(d))
	if !opts.SkipVersion {
		reg(http.MethodGet, "/api/version", makeVersionHandler(opts.Version))
	}

	// Model management — not implementable atop llama-swap.
	notImpl := notImplementedHandler("model management is not implemented; llama-swap routes requests to user-managed processes")
	reg(http.MethodPost, "/api/create", notImpl)
	reg(http.MethodPost, "/api/copy", notImpl)
	reg(http.MethodDelete, "/api/delete", notImpl)
	reg(http.MethodPost, "/api/pull", notImpl)
	reg(http.MethodPost, "/api/push", notImpl)
	reg(http.MethodHead, "/api/blobs/{digest}", notImpl)
	reg(http.MethodPost, "/api/blobs/{digest}", notImpl)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: msg})
}

func notImplementedHandler(msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotImplemented, msg)
	}
}

// --- inference handlers ---

// passthroughOllama forwards the original request body to a backend that
// natively speaks the Ollama API, leaving the /api/* path untouched so the
// upstream's native response flows straight back. Returns true when it handled
// the request.
func passthroughOllama(w http.ResponseWriter, r *http.Request, d Dispatcher, body []byte, model string) bool {
	info, ok := d.FindModel(model)
	if !ok || !info.PassthroughOllama {
		return false
	}
	d.DispatchJSON(w, r, body)
	return true
}

func makeChatHandler(d Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "could not read request body")
			return
		}
		var req ChatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}
		if req.Model == "" {
			writeError(w, http.StatusBadRequest, "missing 'model' field")
			return
		}

		if passthroughOllama(w, r, d, body, req.Model) {
			return
		}

		translated, err := TranslateChatRequest(&req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Ollama defaults stream to true when omitted.
		streaming := true
		if req.Stream != nil {
			streaming = *req.Stream
		}

		r.URL.Path = "/v1/chat/completions"

		if streaming {
			runStreaming(w, r, d, translated, NewChatStreamWriter(w, req.Model))
		} else {
			runBuffered(w, r, d, translated, req.Model, "application/json", TranslateChatResponse)
		}
	}
}

func makeGenerateHandler(d Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "could not read request body")
			return
		}
		var req GenerateRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}
		if req.Model == "" {
			writeError(w, http.StatusBadRequest, "missing 'model' field")
			return
		}

		if passthroughOllama(w, r, d, body, req.Model) {
			return
		}

		translated, err := TranslateGenerateRequest(&req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		streaming := true
		if req.Stream != nil {
			streaming = *req.Stream
		}

		r.URL.Path = "/v1/chat/completions"

		if streaming {
			runStreaming(w, r, d, translated, NewGenerateStreamWriter(w, req.Model))
		} else {
			runBuffered(w, r, d, translated, req.Model, "application/json", TranslateGenerateResponse)
		}
	}
}

func makeEmbedHandler(d Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "could not read request body")
			return
		}
		var req EmbedRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}
		if req.Model == "" {
			writeError(w, http.StatusBadRequest, "missing 'model' field")
			return
		}
		if passthroughOllama(w, r, d, body, req.Model) {
			return
		}
		translated, err := TranslateEmbedRequest(&req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		r.URL.Path = "/v1/embeddings"
		runBuffered(w, r, d, translated, req.Model, "application/json", TranslateEmbedResponse)
	}
}

func makeEmbeddingsHandler(d Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "could not read request body")
			return
		}
		var req EmbeddingsRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}
		if req.Model == "" {
			writeError(w, http.StatusBadRequest, "missing 'model' field")
			return
		}
		if passthroughOllama(w, r, d, body, req.Model) {
			return
		}
		translated, err := TranslateEmbeddingsRequest(&req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		r.URL.Path = "/v1/embeddings"

		// The legacy endpoint ignores total duration; use a translator that
		// drops the model and duration parameters.
		runBuffered(w, r, d, translated, req.Model, "application/json",
			func(body []byte, _ string, _ time.Duration) ([]byte, error) {
				return TranslateEmbeddingsResponse(body)
			})
	}
}

// runStreaming installs sw as the response writer, dispatches, then finalizes.
func runStreaming(w http.ResponseWriter, r *http.Request, d Dispatcher, body []byte, sw *StreamWriter) {
	defer sw.Finalize()
	d.DispatchJSON(sw, r, body)
}

// translator is the signature shared by the non-streaming response converters.
type translator func(openaiBody []byte, model string, totalDuration time.Duration) ([]byte, error)

// runBuffered installs a BufferingWriter, dispatches, then either translates the
// captured 2xx body or passes through whatever error the upstream wrote.
func runBuffered(w http.ResponseWriter, r *http.Request, d Dispatcher, body []byte, model, contentType string, tr translator) {
	bw := NewBufferingWriter(w)
	start := time.Now()
	d.DispatchJSON(bw, r, body)

	status := bw.CapturedStatus()
	if status < 200 || status >= 300 {
		bw.CommitPassThrough()
		return
	}

	translated, err := tr(bw.CapturedBody(), model, time.Since(start))
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("error translating upstream response: %v", err))
		return
	}
	bw.CommitTranslated(translated, contentType, http.StatusOK)
}

// --- informational handlers ---

func makeTagsHandler(d Dispatcher) http.HandlerFunc {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return func(w http.ResponseWriter, r *http.Request) {
		models := d.ListModels()
		resp := TagsResponse{Models: make([]TagModel, 0, len(models))}
		for _, m := range models {
			resp.Models = append(resp.Models, TagModel{
				Name:       m.ID,
				Model:      m.ID,
				ModifiedAt: now,
				Details:    TagDetails{Format: "gguf"},
			})
		}
		sort.Slice(resp.Models, func(i, j int) bool {
			return resp.Models[i].Name < resp.Models[j].Name
		})
		writeJSON(w, http.StatusOK, resp)
	}
}

func makeShowHandler(d Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "could not read request body")
			return
		}
		var req struct {
			Model string `json:"model"`
			Name  string `json:"name"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}
		name := req.Model
		if name == "" {
			name = req.Name
		}
		if name == "" {
			writeError(w, http.StatusBadRequest, "missing 'model' field")
			return
		}
		m, ok := d.FindModel(name)
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("model %q not found", name))
			return
		}
		resp := ShowResponse{
			Modelfile:  fmt.Sprintf("# Model managed by llama-swap as %q\n", m.ID),
			Parameters: "",
			Template:   "",
			Details: TagDetails{
				Format: "gguf",
			},
		}
		// Surface user-provided metadata under model_info so clients that care
		// can read it without us inventing a schema for fields llama-swap doesn't
		// track natively.
		if m.Metadata != nil {
			resp.ModelInfo = map[string]any{"llamaswap": m.Metadata}
		}
		// Best-effort capability hints from metadata; safe to omit.
		if caps := capabilitiesFromMetadata(m.Metadata); len(caps) > 0 {
			resp.Capabilities = caps
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func capabilitiesFromMetadata(meta map[string]any) []string {
	if meta == nil {
		return nil
	}
	out := []string{"completion"}
	if v, _ := meta["vision"].(bool); v {
		out = append(out, "vision")
	}
	if v, _ := meta["tools"].(bool); v {
		out = append(out, "tools")
	}
	if v, _ := meta["embedding"].(bool); v {
		out = append(out, "embedding")
	}
	return out
}

func makePsHandler(d Dispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ids := d.RunningModels()
		future := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano)
		resp := PsResponse{Models: make([]PsModel, 0, len(ids))}
		for _, id := range ids {
			resp.Models = append(resp.Models, PsModel{
				Name:      id,
				Model:     id,
				ExpiresAt: future,
				Details:   TagDetails{Format: "gguf"},
			})
		}
		sort.Slice(resp.Models, func(i, j int) bool {
			return resp.Models[i].Name < resp.Models[j].Name
		})
		writeJSON(w, http.StatusOK, resp)
	}
}

func makeVersionHandler(version string) http.HandlerFunc {
	v := strings.TrimSpace(version)
	if v == "" {
		v = "0.0.0-llama-swap"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, VersionResponse{Version: v})
	}
}
