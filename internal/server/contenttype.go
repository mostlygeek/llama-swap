package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
	"github.com/tidwall/gjson"
)

// bodyKind is the request body format an endpoint expects.
type bodyKind int

const (
	// bodyJSON is for endpoints that read a JSON object with a "model" field.
	bodyJSON bodyKind = iota
	// bodyMultipart is for endpoints that read a multipart/form-data upload.
	bodyMultipart
)

// bodyKindForPath returns the body format a model-dispatched POST endpoint
// expects: multipart for the form routes, JSON for everything else.
func bodyKindForPath(path string) bodyKind {
	if slices.Contains(modelPostFormRoutes, path) {
		return bodyMultipart
	}
	return bodyJSON
}

// requestBodyError is a request body that cannot be used to route the request.
type requestBodyError struct {
	status  int
	message string
}

func (e *requestBodyError) Error() string { return e.message }

func badBody(format string, args ...any) *requestBodyError {
	return &requestBodyError{status: http.StatusBadRequest, message: fmt.Sprintf(format, args...)}
}

func bodyTooLarge(maxBytes int64) *requestBodyError {
	return &requestBodyError{
		status:  http.StatusRequestEntityTooLarge,
		message: fmt.Sprintf("request body exceeds the %d MB limit", maxBytes>>20),
	}
}

// bodyTooLargeFrom returns a 413 error when err came from reading a body past
// the cap set by prepareModelBody, and nil for any other error.
func bodyTooLargeFrom(err error) *requestBodyError {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return bodyTooLarge(tooLarge.Limit)
	}
	return nil
}

// prepareModelBody caps the request body at maxBytes and makes its
// Content-Type match what the endpoint expects. See normalizeBodyContentType.
func prepareModelBody(w http.ResponseWriter, r *http.Request, maxBytes int64) *requestBodyError {
	if r.ContentLength > maxBytes {
		return bodyTooLarge(maxBytes)
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	return normalizeBodyContentType(r, bodyKindForPath(r.URL.Path))
}

// normalizeBodyContentType makes a request's Content-Type match what the
// endpoint expects, so later stages that branch on the header (model lookup,
// request filters, the upstream) all see the same format.
//
// Clients often omit the header or send the wrong one; curl -d, for example,
// sends application/x-www-form-urlencoded with a JSON body. For a bodyJSON
// endpoint a body that parses as a JSON object has its Content-Type set to
// application/json. Bodies that cannot be used return an error that says what
// was expected, rather than a generic "no model id" error.
//
// Requests that already declare the expected type are not read. A form-encoded
// body that carries a "model" field is left alone for the existing form
// handling. Only methods that carry a body are inspected.
func normalizeBodyContentType(r *http.Request, kind bodyKind) *requestBodyError {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return nil
	}

	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "multipart/form-data") {
		return nil
	}
	if kind == bodyJSON && strings.Contains(contentType, "application/json") {
		return nil
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		if bodyErr := bodyTooLargeFrom(err); bodyErr != nil {
			return bodyErr
		}
		return badBody("error reading request body: %v", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return badBody("request body is empty")
	}

	shownType := contentType
	if shownType == "" {
		shownType = "not set"
	}

	if kind == bodyJSON && (trimmed[0] == '{' || trimmed[0] == '[') {
		if !gjson.ValidBytes(trimmed) {
			return badBody("request body is not valid JSON (Content-Type: %s)", shownType)
		}
		r.Header.Set("Content-Type", "application/json")
		return nil
	}

	// Form-encoded bodies with a model field are still routed by the form path.
	if form, _ := url.ParseQuery(string(body)); form.Get("model") != "" {
		return nil
	}

	if kind == bodyMultipart {
		return badBody("no model id could be identified: this endpoint expects multipart/form-data but Content-Type is %s", shownType)
	}
	return badBody("no model id could be identified: Content-Type is %s and the body is neither a JSON object nor a form with a \"model\" field; send JSON with Content-Type: application/json", shownType)
}

// createContentTypeMiddleware returns middleware that tolerates a missing or
// wrong Content-Type on model-dispatched POST requests and caps the body size at
// maxBytes. A JSON body sent with another type is relabelled application/json;
// unusable bodies are rejected with an error that names the expected format.
func createContentTypeMiddleware(maxBytes int64) chain.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if bodyErr := prepareModelBody(w, r, maxBytes); bodyErr != nil {
				swaputil.SendResponse(w, r, bodyErr.status, bodyErr.message)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
