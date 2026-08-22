package swaputil

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tidwall/gjson"
)

// decodeEnvelope parses an error body and fails when "error" is not an object.
func decodeEnvelope(t *testing.T, body []byte) gjson.Result {
	t.Helper()
	if !gjson.ValidBytes(body) {
		t.Fatalf("body is not valid JSON: %s", body)
	}
	parsed := gjson.ParseBytes(body)
	errValue := parsed.Get("error")
	if !errValue.IsObject() {
		t.Fatalf(`"error" is %s, want an object: %s`, errValue.Type, body)
	}
	return parsed
}

func TestErrorEnvelope_OpenAIShape(t *testing.T) {
	body := NewErrorEnvelope(http.StatusNotFound, "model not found", "").JSON()
	parsed := decodeEnvelope(t, body)

	if got := parsed.Get("error.message").String(); got != "model not found" {
		t.Errorf("error.message = %q, want %q", got, "model not found")
	}
	if got := parsed.Get("error.type").String(); got != ErrorTypeInvalidRequest {
		t.Errorf("error.type = %q, want %q", got, ErrorTypeInvalidRequest)
	}
	if got := parsed.Get("error.code").String(); got != "not_found" {
		t.Errorf("error.code = %q, want %q", got, "not_found")
	}
	if !parsed.Get("error.param").Exists() || parsed.Get("error.param").Type != gjson.Null {
		t.Errorf("error.param = %v, want null", parsed.Get("error.param"))
	}
	if got := parsed.Get("src").String(); got != "llama-swap" {
		t.Errorf("src = %q, want llama-swap", got)
	}
}

func TestErrorEnvelope_TypesByStatus(t *testing.T) {
	tests := []struct {
		status   int
		wantType string
		wantCode string
	}{
		{http.StatusBadRequest, ErrorTypeInvalidRequest, "bad_request"},
		{http.StatusUnauthorized, ErrorTypeAuthentication, "unauthorized"},
		{http.StatusForbidden, ErrorTypeAuthentication, "forbidden"},
		{http.StatusNotFound, ErrorTypeInvalidRequest, "not_found"},
		{http.StatusConflict, ErrorTypeInvalidRequest, "conflict"},
		{http.StatusTooManyRequests, ErrorTypeRateLimit, "rate_limit_exceeded"},
		{http.StatusBadGateway, ErrorTypeServer, "bad_gateway"},
		{http.StatusServiceUnavailable, ErrorTypeServer, "service_unavailable"},
		{http.StatusGatewayTimeout, ErrorTypeServer, "gateway_timeout"},
		{http.StatusInternalServerError, ErrorTypeServer, "internal_error"},
		{http.StatusTeapot, ErrorTypeInvalidRequest, "invalid_request"},
	}

	for _, tc := range tests {
		env := NewErrorEnvelope(tc.status, "boom", "")
		if env.Error.Type != tc.wantType {
			t.Errorf("status %d: type = %q, want %q", tc.status, env.Error.Type, tc.wantType)
		}
		if env.Error.Code != tc.wantCode {
			t.Errorf("status %d: code = %q, want %q", tc.status, env.Error.Code, tc.wantCode)
		}
	}
}

func TestErrorEnvelope_CodeOverride(t *testing.T) {
	env := NewErrorEnvelope(http.StatusTooManyRequests, "boom", "custom_code")
	if env.Error.Code != "custom_code" {
		t.Errorf("code = %q, want custom_code", env.Error.Code)
	}
	if env.Error.Type != ErrorTypeRateLimit {
		t.Errorf("type = %q, want %q", env.Error.Type, ErrorTypeRateLimit)
	}
}

func TestErrorEnvelope_MarshalFallbackIsValid(t *testing.T) {
	var envelope ErrorEnvelope
	if err := json.Unmarshal([]byte(marshalFallback), &envelope); err != nil {
		t.Fatalf("marshalFallback does not decode into an ErrorEnvelope: %v", err)
	}
	if envelope.Src != "llama-swap" || envelope.Error.Message == "" || envelope.Error.Type != ErrorTypeServer {
		t.Errorf("marshalFallback = %+v, want a filled llama-swap server error", envelope)
	}
}

func TestConcurrencyLimitError_Body(t *testing.T) {
	err := ConcurrencyLimitError{}
	parsed := decodeEnvelope(t, err.Body())

	if got := parsed.Get("error.message").String(); got != "Too many requests" {
		t.Errorf("error.message = %q, want %q", got, "Too many requests")
	}
	if got := parsed.Get("error.type").String(); got != ErrorTypeRateLimit {
		t.Errorf("error.type = %q, want %q", got, ErrorTypeRateLimit)
	}
	if got := parsed.Get("error.code").String(); got != "concurrency_limit" {
		t.Errorf("error.code = %q, want %q", got, "concurrency_limit")
	}
	if got := err.StatusCode(); got != http.StatusTooManyRequests {
		t.Errorf("StatusCode() = %d, want 429", got)
	}
	if got := err.Header().Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %q, want 1", got)
	}
}

func TestConcurrencyLimitError_CustomMessageAndRetryAfter(t *testing.T) {
	err := ConcurrencyLimitError{RetryAfter: 30, Message: "slow down"}
	parsed := decodeEnvelope(t, err.Body())

	if got := parsed.Get("error.message").String(); got != "slow down" {
		t.Errorf("error.message = %q, want %q", got, "slow down")
	}
	if got := err.Header().Get("Retry-After"); got != "30" {
		t.Errorf("Retry-After = %q, want 30", got)
	}
}
