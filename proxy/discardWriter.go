package proxy

import "net/http"

// Custom discard writer that implements http.ResponseWriter but just discards everything
type DiscardWriter struct {
	header http.Header
	status int
}

func (w *DiscardWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *DiscardWriter) Write(data []byte) (int, error) {
	return len(data), nil
}

func (w *DiscardWriter) WriteHeader(code int) {
	w.status = code
}

// Satisfy the http.Flusher interface for streaming responses
func (w *DiscardWriter) Flush() {}
