package server

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ensureStreamUsage sets stream_options.include_usage on streaming completion
// requests so OpenAI-compatible backends (e.g. vllm) emit a usage object in the
// stream; without it they report no token counts at all. llama.cpp already
// includes timings regardless, so this is harmless there. It leaves the body
// untouched when it is not a streaming request or the client already set
// stream_options. Returns the updated body and true only when a change was made.
func ensureStreamUsage(body []byte) ([]byte, bool) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return nil, false
	}
	if !gjson.GetBytes(body, "stream").Bool() {
		return nil, false
	}
	if gjson.GetBytes(body, "stream_options.include_usage").Exists() {
		return nil, false
	}
	updated, err := sjson.SetBytes(body, "stream_options.include_usage", true)
	if err != nil {
		return nil, false
	}
	return updated, true
}

// CreateMetricsMiddleware returns middleware that records token metrics for
// model-dispatched POST requests. It resolves the model, tees the response into
// a buffer, and parses token usage once the upstream handler returns.
//
// /v1/realtime websocket upgrades are recorded too, once per session when the
// socket closes; see realtime.go.
func CreateMetricsMiddleware(mm *metricsMonitor, cfg config.Config) chain.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			websocket := r.Method == http.MethodGet && swaputil.IsWebSocketUpgrade(r)
			if mm == nil || (r.Method != http.MethodPost && !websocket) {
				next.ServeHTTP(w, r)
				return
			}

			// Determine the model-routed endpoint path. Regular routes are
			// already meterable; /upstream/<model>/<path> is metered only when
			// the remaining path matches a model-dispatched endpoint.
			checkPath := r.URL.Path
			if strings.HasPrefix(r.URL.Path, "/upstream/") {
				var found bool
				_, _, checkPath, found = swaputil.FindModelInPath(cfg, strings.TrimPrefix(r.URL.Path, "/upstream"))
				if !found {
					next.ServeHTTP(w, r)
					return
				}
			}

			task, metered := meteredTask(checkPath)
			if !metered || websocket != (task == hint.TaskRealtime) {
				next.ServeHTTP(w, r)
				return
			}

			// Resolve the model now so downstream dispatch hits the context
			// fast path; FetchContext restores the request body for regular
			// routes and extracts the model from the URL for /upstream routes.
			data, err := swaputil.FetchContext(r, cfg)
			if err != nil {
				swaputil.SendError(w, r, swaputil.ErrNoModelInContext)
				return
			}

			// Buffer the request body/headers for capture before dispatch
			// consumes them. The body is also read when it is a JSON request so
			// stream_options can be injected for streaming backends like vllm.
			cf := captureFieldsFor(checkPath)
			isJSON := strings.Contains(r.Header.Get("Content-Type"), "application/json")
			var reqBody []byte
			var reqHeaders map[string]string
			if r.Body != nil && (isJSON || (mm.enableCaptures && cf&captureReqBody != 0)) {
				buffered, err := io.ReadAll(r.Body)
				if bodyErr := bodyTooLargeFrom(err); bodyErr != nil {
					swaputil.SendResponse(w, r, bodyErr.status, bodyErr.message)
					return
				}
				if err == nil {
					reqBody = buffered
					r.Body.Close()
					r.Body = io.NopCloser(bytes.NewReader(reqBody))
				}
			}
			if mm.enableCaptures && cf&captureReqHeaders != 0 {
				reqHeaders = headerMap(r.Header)
				redactHeaders(reqHeaders)
			}

			// Inject stream_options.include_usage so streaming backends report
			// token usage. reqBody keeps the original body for capture.
			if isJSON {
				if injected, ok := ensureStreamUsage(reqBody); ok {
					r.Body = io.NopCloser(bytes.NewReader(injected))
					r.ContentLength = int64(len(injected))
					r.Header.Set("Content-Length", strconv.Itoa(len(injected)))
				}
			}

			// Restrict Accept-Encoding to encodings we can decompress so the
			// buffered response body stays parseable.
			if ae := r.Header.Get("Accept-Encoding"); ae != "" {
				r.Header.Set("Accept-Encoding", filterAcceptEncoding(ae))
			}

			// The router or peer proxy fills the hint in while dispatching:
			// where the request went and what kind of server answered it.
			ctx, _ := hint.New(r.Context())
			r = r.WithContext(ctx)

			recorder := newBodyCopier(w)
			if websocket {
				recorder.realtime = newRealtimeSession()
			}
			// For a websocket this returns when the session ends.
			next.ServeHTTP(recorder, r)
			// A request abandoned before any response was written must not be
			// filed as a successful (empty-body) metric. See #1029.
			swaputil.MarkClientClosed(recorder, r)
			if websocket && recorder.Status() == http.StatusSwitchingProtocols {
				mm.recordRealtime(data.ModelID, task, r, recorder)
				return
			}
			// A refused upgrade (model failed to start, upstream error) is an
			// ordinary failed request.
			mm.record(data.ModelID, task, r, recorder, cf, reqBody, reqHeaders)
		})
	}
}
