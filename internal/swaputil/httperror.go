package swaputil

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// Error types mirror the values the OpenAI API uses for error.type. Clients
// branch on these to decide whether a request is retryable.
const (
	ErrorTypeInvalidRequest = "invalid_request_error"
	ErrorTypeAuthentication = "authentication_error"
	ErrorTypeRateLimit      = "rate_limit_error"
	ErrorTypeServer         = "server_error"
)

// ErrorDetail is the object-valued "error" field of an OpenAI-compatible error
// response. Clients written against the OpenAI API read it as
// body["error"]["message"], so the field must never be a bare string.
type ErrorDetail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    string  `json:"code,omitempty"`
}

// ErrorEnvelope is the JSON body llama-swap returns for every error response.
// "error" is the OpenAI-compatible object above; "src" marks llama-swap, rather
// than an upstream inference server, as the origin of the error.
type ErrorEnvelope struct {
	Src   string      `json:"src"`
	Error ErrorDetail `json:"error"`
}

// marshalFallback is written when an envelope somehow fails to marshal. It is
// the same shape as a marshalled ErrorEnvelope so clients never see a body they
// cannot parse.
const marshalFallback = `{"src":"llama-swap","error":{"message":"failed to marshal response","type":"server_error","param":null,"code":"internal_error"}}`

// NewErrorEnvelope builds an error envelope for an HTTP status. The error's
// type and code are derived from status; a non-empty code overrides the derived
// one for errors that have a more specific cause (e.g. a concurrency limit
// rather than a generic rate limit).
func NewErrorEnvelope(status int, message, code string) ErrorEnvelope {
	errType, defaultCode := errorTypeForStatus(status)
	if code == "" {
		code = defaultCode
	}
	return ErrorEnvelope{
		Src: "llama-swap",
		Error: ErrorDetail{
			Message: message,
			Type:    errType,
			Code:    code,
		},
	}
}

// JSON renders the envelope as an HTTP response body.
func (e ErrorEnvelope) JSON() []byte {
	b, err := json.Marshal(e)
	if err != nil {
		return []byte(marshalFallback)
	}
	return b
}

// errorTypeForStatus maps an HTTP status onto the OpenAI error type and a
// default code for that status.
func errorTypeForStatus(status int) (errType string, code string) {
	switch status {
	case http.StatusBadRequest:
		return ErrorTypeInvalidRequest, "bad_request"
	case http.StatusUnauthorized:
		return ErrorTypeAuthentication, "unauthorized"
	case http.StatusForbidden:
		return ErrorTypeAuthentication, "forbidden"
	case http.StatusNotFound:
		return ErrorTypeInvalidRequest, "not_found"
	case http.StatusConflict:
		return ErrorTypeInvalidRequest, "conflict"
	case http.StatusTooManyRequests:
		return ErrorTypeRateLimit, "rate_limit_exceeded"
	case http.StatusBadGateway:
		return ErrorTypeServer, "bad_gateway"
	case http.StatusServiceUnavailable:
		return ErrorTypeServer, "service_unavailable"
	case http.StatusGatewayTimeout:
		return ErrorTypeServer, "gateway_timeout"
	}

	if status >= 400 && status < 500 {
		return ErrorTypeInvalidRequest, "invalid_request"
	}
	return ErrorTypeServer, "internal_error"
}

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

	// Message overrides the JSON body's error message. Defaults to
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
	return NewErrorEnvelope(e.StatusCode(), e.message(), "concurrency_limit").JSON()
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
