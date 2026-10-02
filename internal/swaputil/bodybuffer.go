package swaputil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// bodyBuffer holds a request's body bytes so each stage in the middleware
// chain can read them without making its own copy.
type bodyBuffer struct {
	data []byte
}

type bodyBufferKey struct{}

// BufferRequestBody reads r's body once, capped at MaxRequestBodySize, and
// returns a request that carries the bytes. Later RequestBody calls reuse them
// instead of copying the body again, and ReplaceRequestBody keeps them current.
// It does nothing when r already carries a buffered body. A body over the cap
// returns a *RequestBodyError with status 413.
func BufferRequestBody(w http.ResponseWriter, r *http.Request) (*http.Request, error) {
	if _, ok := r.Context().Value(bodyBufferKey{}).(*bodyBuffer); ok {
		return r, nil
	}

	var data []byte
	if r.Body != nil && r.Body != http.NoBody {
		var err error
		data, err = io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBodySize))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return r, &RequestBodyError{
					Message: fmt.Sprintf("request body exceeds the %d MB limit", MaxRequestBodySize>>20),
					Status:  http.StatusRequestEntityTooLarge,
				}
			}
			return r, fmt.Errorf("error reading request body: %w", err)
		}
	}

	r = r.WithContext(context.WithValue(r.Context(), bodyBufferKey{}, &bodyBuffer{data: data}))
	r.Body = io.NopCloser(bytes.NewReader(data))
	return r, nil
}

// RequestBody returns the request body bytes and leaves r.Body readable from
// the start. With a buffered request the bytes are shared, not copied, so the
// caller must not modify them; use ReplaceRequestBody to change the body.
// Without BufferRequestBody it reads the body and puts it back.
func RequestBody(r *http.Request) ([]byte, error) {
	if buf, ok := r.Context().Value(bodyBufferKey{}).(*bodyBuffer); ok {
		r.Body = io.NopCloser(bytes.NewReader(buf.data))
		return buf.data, nil
	}
	if r.Body == nil {
		return nil, nil
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	return data, nil
}

// ReplaceRequestBody swaps in a new body and fixes the length headers so the
// upstream sees consistent framing. It updates the buffered bytes, if any.
func ReplaceRequestBody(r *http.Request, body []byte) {
	if buf, ok := r.Context().Value(bodyBufferKey{}).(*bodyBuffer); ok {
		buf.data = body
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.Header.Del("Transfer-Encoding")
	r.Header.Set("Content-Length", strconv.Itoa(len(body)))
	r.ContentLength = int64(len(body))
}
