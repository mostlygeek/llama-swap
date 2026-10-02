package swaputil

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tidwall/gjson"
)

// BodyKind is the request body format an endpoint expects.
type BodyKind int

const (
	// BodyJSON is for endpoints that read a JSON object with a "model" field.
	BodyJSON BodyKind = iota
	// BodyMultipart is for endpoints that read a multipart/form-data upload.
	BodyMultipart
)

// MaxRequestBodySize caps the request bodies read for model-dispatched POSTs.
const MaxRequestBodySize = 250 << 20

// RequestBodyError reports a request body that cannot be used to route the
// request. SendError renders it with Status, or 400 when Status is zero.
type RequestBodyError struct {
	Message string
	Status  int
}

// LimitRequestBody caps r.Body at MaxRequestBodySize. Readers past the cap get
// an *http.MaxBytesError.
func LimitRequestBody(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodySize)
}

func (e *RequestBodyError) Error() string { return e.Message }

// NormalizeBodyContentType makes a request's Content-Type match what the
// endpoint expects, so later stages that branch on the header (model lookup,
// request filters, the upstream) all see the same format.
//
// Clients often omit the header or send the wrong one; curl -d, for example,
// sends application/x-www-form-urlencoded with a JSON body. For a BodyJSON
// endpoint a body that parses as a JSON object has its Content-Type set to
// application/json. Bodies that cannot be used return a *RequestBodyError that
// says what was expected, rather than a generic "no model id" error.
//
// Requests that already declare the expected type are not read. A form-encoded
// body that carries a "model" field is left alone for the existing form
// handling. Only methods that carry a body are inspected.
func NormalizeBodyContentType(r *http.Request, kind BodyKind) error {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return nil
	}

	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "multipart/form-data") {
		return nil
	}
	if kind == BodyJSON && strings.Contains(contentType, "application/json") {
		return nil
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return &RequestBodyError{
				Message: fmt.Sprintf("request body exceeds the %d MB limit", MaxRequestBodySize>>20),
				Status:  http.StatusRequestEntityTooLarge,
			}
		}
		return fmt.Errorf("error reading request body: %w", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return &RequestBodyError{Message: "request body is empty"}
	}

	looksLikeJSON := trimmed[0] == '{' || trimmed[0] == '['
	if kind == BodyJSON && looksLikeJSON {
		if !gjson.ValidBytes(trimmed) {
			return &RequestBodyError{Message: fmt.Sprintf(
				"request body is not valid JSON (Content-Type: %s)", describeContentType(contentType))}
		}
		r.Header.Set("Content-Type", "application/json")
		return nil
	}

	// Form-encoded bodies with a model field are still routed by the form path.
	if form, _ := url.ParseQuery(string(body)); form.Get("model") != "" {
		return nil
	}

	if kind == BodyMultipart {
		return &RequestBodyError{Message: fmt.Sprintf(
			"no model id could be identified: this endpoint expects multipart/form-data but Content-Type is %s",
			describeContentType(contentType))}
	}
	return &RequestBodyError{Message: fmt.Sprintf(
		"no model id could be identified: Content-Type is %s and the body is neither a JSON object nor a form with a \"model\" field; send JSON with Content-Type: application/json",
		describeContentType(contentType))}
}

func describeContentType(contentType string) string {
	if contentType == "" {
		return "not set"
	}
	return contentType
}
