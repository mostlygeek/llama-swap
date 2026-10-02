package swaputil

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeBodyContentType_StreamedBodyTooLarge(t *testing.T) {
	// No Content-Length, so only the reader-side cap can catch it.
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", io.LimitReader(zeroReader{}, MaxRequestBodySize+1))
	LimitRequestBody(httptest.NewRecorder(), r)

	err := NormalizeBodyContentType(r, BodyJSON)
	var bodyErr *RequestBodyError
	if !errors.As(err, &bodyErr) || bodyErr.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("err = %v, want RequestBodyError with status 413", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
