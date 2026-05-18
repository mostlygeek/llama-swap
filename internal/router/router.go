package router

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/proxy/config"
	"github.com/tidwall/gjson"
)

type contextkey struct {
	name string
}

var (
	ErrNoModelInContext  = fmt.Errorf("no model in request context")
	ErrNoRouterFound     = fmt.Errorf("no router found for model")
	ErrNoPeerModelFound  = fmt.Errorf("peer model not found")
	ErrNoLocalModelFound = fmt.Errorf("local model not found")

	// Context keys use to store information info in the request's context
	ModelKey   = &contextkey{"model"}    // the model value in the request
	ModelIDKey = &contextkey{"model-id"} // The model ID in the configuration
)

type Router interface {
	// Shutdown blocks until the router has shutdown returning nil
	// when the router has shutdown successfully.
	//
	// timeout controls how long to wait for inflight requests to finish. After
	// the timeout all inflight requests will be cancelled.
	Shutdown(timeout time.Duration) error

	// ServeHTTP implements the http.Handler and requests coming in will
	// trigger any model swapping and routing logic.
	ServeHTTP(http.ResponseWriter, *http.Request)

	// Handles reports whether this router can serve requests for the given model.
	Handles(model string) bool
}

// FetchModel will attempt to get the model id from the context then
// from the model body. If it extracts the model from the body it will
// store the model in the context for downstream handlers. An error
// will be returned when model can not be fetch from either location.
func FetchModel(r *http.Request, cfg config.Config) (string, string, error) {
	model, realName, ok := GetModel(r.Context())
	if ok {
		return model, realName, nil
	}

	if model, err := ExtractModel(r); err == nil {
		realName, _ := cfg.RealModelName(model)
		if realName == "" {
			realName = model
		}
		*r = *r.WithContext(SetModel(r.Context(), model, realName))
		return model, realName, nil
	}

	return "", "", ErrNoModelInContext
}

func SetModel(ctx context.Context, requested, real string) context.Context {
	ctx = context.WithValue(ctx, ModelKey, requested)
	ctx = context.WithValue(ctx, ModelIDKey, real)
	return ctx
}

func GetModel(ctx context.Context) (string, string, bool) {
	requested, ok := ctx.Value(ModelKey).(string)
	real, _ := ctx.Value(ModelIDKey).(string)
	return requested, real, ok
}

// ExtractModel pulls the model name from an HTTP request without consuming the
// body. For GET requests it reads the "model" query parameter. For POST
// requests it inspects Content-Type and parses JSON, multipart/form-data, or
// application/x-www-form-urlencoded bodies. The request body is always restored
// before returning so downstream handlers — including reverse proxies that
// forward raw bytes upstream — can still read it.
func ExtractModel(r *http.Request) (string, error) {
	if r.Method == http.MethodGet {
		if model := r.URL.Query().Get("model"); model != "" {
			return model, nil
		}
		return "", fmt.Errorf("missing 'model' query parameter")
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return "", fmt.Errorf("error reading request body: %w", err)
	}
	defer func() {
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}()

	contentType := r.Header.Get("Content-Type")

	if strings.Contains(contentType, "application/json") {
		model := gjson.GetBytes(bodyBytes, "model").String()
		if model == "" {
			return "", fmt.Errorf("missing or empty 'model' in JSON body")
		}
		return model, nil
	}

	// Form parsers read from r.Body, so feed them a fresh reader over the
	// buffered bytes. The deferred restore above will reset r.Body again
	// after parsing.
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	if strings.Contains(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return "", fmt.Errorf("error parsing multipart form: %w", err)
		}
	} else {
		if err := r.ParseForm(); err != nil {
			return "", fmt.Errorf("error parsing form: %w", err)
		}
	}

	if model := r.FormValue("model"); model != "" {
		return model, nil
	}

	return "", fmt.Errorf("missing 'model' parameter")
}

func SendError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNoModelInContext):
		SendResponse(w, r, http.StatusNotFound, "no model id could be identified")
	case errors.Is(err, ErrNoPeerModelFound):
		SendResponse(w, r, http.StatusNotFound, "no peer found for requested model")
	case errors.Is(err, ErrNoLocalModelFound):
		SendResponse(w, r, http.StatusNotFound, "no local server found for requested model")
	case errors.Is(err, ErrNoRouterFound):
		SendResponse(w, r, http.StatusNotFound, "no router for requested model")
	default:
		SendResponse(w, r, http.StatusInternalServerError, fmt.Sprintf("unspecific error: %v", err))
	}
}

// SendResponse detects what content type the client prefers and returns an error response in that format.
func SendResponse(w http.ResponseWriter, r *http.Request, status int, message string) {
	// Check Accept header for preferred response format
	acceptHeader := r.Header.Get("Accept")
	if strings.Contains(acceptHeader, "text/plain") {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		w.Write([]byte(fmt.Sprintf("llama-swap: %s", message)))
		return
	}

	if strings.Contains(acceptHeader, "text/html") {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(status)
		w.Write([]byte(fmt.Sprintf(`<html><body><h1>llama-swap</h1><p>%s</p></body></html>`, message)))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write([]byte(fmt.Sprintf(`{"src":"llama-swap", "error": "%s"}`, message)))
}
