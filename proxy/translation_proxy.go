package proxy

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	xl "github.com/mostlygeek/llama-swap/proxy/translate"
)

// resolveModelProtocols returns the ordered list of protocols the model
// identified by modelID natively supports. When matrix is in use, it
// intersects across all candidate physical models.
func resolveModelProtocols(pm *ProxyManager, modelID string) []xl.Protocol {
	if pm.matrix != nil {
		if ps := pm.matrix.ProtocolsFor(modelID); len(ps) > 0 {
			return toProtocols(ps)
		}
	}
	if m, ok := pm.config.Models[modelID]; ok {
		return toProtocols(m.Protocols)
	}
	return []xl.Protocol{xl.ProtocolOpenAI, xl.ProtocolAnthropic}
}

func toProtocols(ss []string) []xl.Protocol {
	out := make([]xl.Protocol, 0, len(ss))
	for _, s := range ss {
		out = append(out, xl.Protocol(s))
	}
	return out
}

// responseTranslator buffers the upstream response body, then translates it
// into the client's protocol before forwarding. Used for non-streaming
// responses only; streaming is handled by a dedicated writer wrapper
// installed elsewhere.
type responseTranslator struct {
	gin.ResponseWriter
	inbound xl.Protocol
	target  xl.Protocol
	logger  interface {
		Errorf(string, ...any)
		Debugf(string, ...any)
	}
	buf         bytes.Buffer
	status      int
	headersSent bool
}

// WriteHeader is intentionally captured; we rewrite headers on flushTo.
func (r *responseTranslator) WriteHeader(code int) {
	r.status = code
}

func (r *responseTranslator) Write(p []byte) (int, error) {
	return r.buf.Write(p)
}

func (r *responseTranslator) WriteString(s string) (int, error) {
	return r.buf.WriteString(s)
}

// Flush is a no-op; we flush once on completion.
func (r *responseTranslator) Flush() {}

// flushTo translates the buffered body and writes the final response to the
// original writer.
func (r *responseTranslator) flushTo(orig gin.ResponseWriter) {
	body := r.buf.Bytes()
	status := r.status
	if status == 0 {
		status = http.StatusOK
	}

	// Non-2xx: pass-through in the upstream's body verbatim, but still
	// rewrite Content-Type to the client's expected media type so the
	// client isn't confused about where the error came from.
	if status < 200 || status >= 300 {
		r.writeOriginal(orig, status, body, upstreamContentType(r.target))
		return
	}

	translated, err := xl.TranslateResponse(r.inbound, r.target, body)
	if err != nil {
		r.logger.Errorf("translate response %s→%s: %v; forwarding upstream body verbatim", r.target, r.inbound, err)
		r.writeOriginal(orig, http.StatusBadGateway, []byte(fmt.Sprintf(`{"error":"translate response failed: %s"}`, err.Error())), upstreamContentType(r.inbound))
		return
	}
	r.writeOriginal(orig, status, translated, upstreamContentType(r.inbound))
}

func (r *responseTranslator) writeOriginal(orig gin.ResponseWriter, status int, body []byte, ct string) {
	h := orig.Header()
	h.Del("Content-Length")
	for k := range h {
		if strings.EqualFold(k, "Transfer-Encoding") {
			h.Del(k)
		}
	}
	h.Set("Content-Type", ct)
	h.Set("Content-Length", strconv.Itoa(len(body)))
	orig.WriteHeader(status)
	_, _ = orig.Write(body)
}

// upstreamContentType returns the JSON media type for a non-streaming body
// in the given protocol.
func upstreamContentType(p xl.Protocol) string {
	return "application/json"
}
