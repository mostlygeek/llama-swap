package shared

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// HTTPError is an error that carries a complete HTTP response. A producer (e.g.
// a scheduler shedding a request) returns one of these; a renderer (e.g.
// router.SendError) writes the status, headers, and body verbatim instead of
// mapping the error to a generic status. It is the seam that lets a component
// shed a request with a rich response (e.g. a 429 with rate-limit headers and a
// JSON hint body) without the renderer knowing the producer's internals.
type HTTPError interface {
	error
	StatusCode() int
	Header() http.Header
	Body() []byte
}

// ConcurrencyLimitError is an HTTPError for a 429 concurrency-limit rejection.
// Zero-value fields fall back to sensible defaults: a 1-second Retry-After and a
// JSON hint body.
type ConcurrencyLimitError struct {
	// RetryAfter, when > 0, is sent as the Retry-After header (in seconds).
	// Defaults to 1.
	RetryAfter int

	// Message overrides the JSON body's "error" field. Defaults to
	// "Too many requests".
	Message string
}

func (e ConcurrencyLimitError) Error() string { return "concurrency limit reached" }

func (e ConcurrencyLimitError) StatusCode() int { return http.StatusTooManyRequests }

func (e ConcurrencyLimitError) Header() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Retry-After", e.retryAfter())
	return h
}

func (e ConcurrencyLimitError) Body() []byte {
	b, _ := json.Marshal(map[string]string{"error": e.message()})
	return b
}

func (e ConcurrencyLimitError) retryAfter() string {
	if e.RetryAfter > 0 {
		return strconv.Itoa(e.RetryAfter)
	}
	return "1"
}

func (e ConcurrencyLimitError) message() string {
	if e.Message != "" {
		return e.Message
	}
	return "Too many requests"
}
