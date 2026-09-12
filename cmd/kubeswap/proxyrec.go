package main

import "net/http"

// statusRecorder records whether the response has started, so proxy error
// handling does not write a second status line.
type statusRecorder struct {
	http.ResponseWriter
	wrote bool
}

// WriteHeader Records that the response has started, then forwards the status code.
func (r *statusRecorder) WriteHeader(code int) {
	r.wrote = true
	r.ResponseWriter.WriteHeader(code)
}

// Write Records that the response has started, then writes the body.
func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.ResponseWriter.Write(b)
}

// Flush implements http.Flusher for streaming responses.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
