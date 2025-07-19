package proxy

import (
	"bytes"
	"net/http"
)

// ResponseRecorder is a custom response recorder that implements http.ResponseWriter
// to capture response data without using httptest.NewRecorder
type ResponseRecorder struct {
	http.ResponseWriter
	body   bytes.Buffer
	header http.Header
	status int
}

func NewResponseRecorder(writer http.ResponseWriter) *ResponseRecorder {
	return &ResponseRecorder{
		ResponseWriter: writer,
		body:           bytes.Buffer{},
		header:         make(http.Header),
		status:         http.StatusOK,
	}
}

func (r *ResponseRecorder) Write(b []byte) (int, error) {
	return r.body.Write(b)
}

func (r *ResponseRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
}

func (r *ResponseRecorder) WriteToOriginal() {
	// Copy headers to original writer
	for k, v := range r.header {
		r.ResponseWriter.Header()[k] = v
	}
	r.ResponseWriter.WriteHeader(r.status)
	r.ResponseWriter.Write(r.body.Bytes())
}
