package proxy

import "net/http"

// Custom discard writer that implements http.ResponseWriter but just discards everything
type DiscardWriter struct{}

func (w *DiscardWriter) Header() http.Header {
	return make(http.Header)
}

func (w *DiscardWriter) Write([]byte) (int, error) {
	return 0, nil // Discard all writes
}

func (w *DiscardWriter) WriteHeader(int) {
	// Ignore status codes
}
