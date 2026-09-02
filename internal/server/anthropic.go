package server

import (
	"bytes"
	"io"
	"net/http"
	"strconv"

	"github.com/tidwall/gjson"

	"github.com/mostlygeek/llama-swap/internal/apiconv"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// dispatchTranslated forwards an already-translated (OpenAI-shaped) body through
// the request-filter + token-metrics + routing pipeline, writing the upstream
// response to w. Callers that need response translation install a translating
// writer as w before calling this, so token metrics tees the raw OpenAI bytes
// while the client receives the translated shape.
func (s *Server) dispatchTranslated(w http.ResponseWriter, r *http.Request, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Del("Transfer-Encoding")
	r.Header.Set("Content-Length", strconv.Itoa(len(body)))
	s.translatedDispatch.ServeHTTP(w, r)
}

// handleAnthropicMessages serves the Anthropic Messages API (/v1/messages). An
// inbound Anthropic request is translated to OpenAI /v1/chat/completions and the
// upstream OpenAI response is translated back to Anthropic shape (streaming and
// buffered). A model that natively speaks Anthropic opts out via the
// passthroughAnthropic config flag; unknown/peer models are forwarded unchanged.
func (s *Server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		swaputil.SendResponse(w, r, http.StatusBadRequest, "could not read request body")
		return
	}
	_ = r.Body.Close()

	requestedModel := gjson.GetBytes(body, "model").String()
	if requestedModel == "" {
		swaputil.SendResponse(w, r, http.StatusBadRequest, "missing or invalid 'model' key")
		return
	}

	// Translate only when the model resolves to a local model that does not opt
	// out via passthroughAnthropic. Unknown or peer models are forwarded as-is.
	translate := false
	if modelID, ok := s.cfg.RealModelName(requestedModel); ok {
		if mc, ok := s.cfg.Models[modelID]; !ok || !mc.PassthroughAnthropic {
			translate = true
		}
	}

	if !translate {
		s.dispatchTranslated(w, r, body)
		return
	}

	converted, terr := apiconv.AnthropicToOpenAIRequest(body)
	if terr != nil {
		swaputil.SendResponse(w, r, http.StatusBadRequest, "error translating Anthropic request to OpenAI: "+terr.Error())
		return
	}
	r.URL.Path = "/v1/chat/completions"

	// Capture streaming intent in the client's terms before translation.
	if gjson.GetBytes(body, "stream").Bool() {
		sw := apiconv.NewAnthropicStreamWriter(w, requestedModel)
		s.dispatchTranslated(sw, r, converted)
		sw.Finalize()
		return
	}

	bw := apiconv.NewBufferingWriter(w)
	s.dispatchTranslated(bw, r, converted)
	if status := bw.CapturedStatus(); status < 200 || status >= 300 {
		bw.CommitPassThrough() // preserve the upstream error body
		return
	}
	translated, terr := apiconv.OpenAIToAnthropicResponse(bw.CapturedBody(), requestedModel)
	if terr != nil {
		s.proxylog.Errorf("error translating Anthropic response for model %s: %s", requestedModel, terr.Error())
		bw.CommitPassThrough()
		return
	}
	bw.CommitTranslated(translated, "application/json", http.StatusOK)
}
