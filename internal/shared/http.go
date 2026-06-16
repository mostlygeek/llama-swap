package shared

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/tidwall/gjson"
)

type contextkey struct {
	name string
}

type ReqContextData struct {
	ApiKey           string
	Model            string
	ModelID          string
	Streaming        bool
	SendLoadingState bool
}

var (
	ReqContextKey        = &contextkey{"context"}
	ErrNoModelInContext  = fmt.Errorf("no model in request context")
	ErrNoRouterFound     = fmt.Errorf("no router found for model")
	ErrNoPeerModelFound  = fmt.Errorf("peer model not found")
	ErrNoLocalModelFound = fmt.Errorf("local model not found")
	ErrConcurrencyLimit  = fmt.Errorf("concurrency limit reached")
)

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
	case errors.Is(err, ErrConcurrencyLimit):
		SendResponse(w, r, http.StatusTooManyRequests, "too many concurrent requests")
	default:
		SendResponse(w, r, http.StatusInternalServerError, fmt.Sprintf("unspecific error: %v", err))
	}
}

// SendResponse detects what content type the client prefers and returns an error response in that format.
func SendResponse(w http.ResponseWriter, r *http.Request, status int, message string) {
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
		w.Write([]byte(fmt.Sprintf(`<html><body><h1>llama-swap</h1><p>%s</p></body></html>`, html.EscapeString(message))))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp, err := json.Marshal(map[string]string{"src": "llama-swap", "error": message})
	if err != nil {
		w.Write([]byte(`{"src":"llama-swap", "error": "failed to marshal response"}`))
		return
	}
	w.Write(resp)
}

// FetchContext will attempt to get the model id from the context then
// from the model body. If it extracts the model from the body it will
// store the model in the context for downstream handlers. An error
// will be returned when model can not be fetch from either location.
func FetchContext(r *http.Request, cfg config.Config) (ReqContextData, error) {
	data, ok := ReadContext(r.Context())
	if ok {
		return data, nil
	}

	if data, err := extractContext(r); err == nil && data.Model != "" {
		realName, _ := cfg.RealModelName(data.Model)
		if realName == "" {
			realName = data.Model
		}
		data.ModelID = realName
		if mc, ok := cfg.Models[realName]; ok {
			data.SendLoadingState = mc.SendLoadingState != nil && *mc.SendLoadingState
		}
		*r = *r.WithContext(SetContext(r.Context(), data))
		return data, nil
	}

	return ReqContextData{}, ErrNoModelInContext
}

func SetContext(ctx context.Context, data ReqContextData) context.Context {
	return context.WithValue(ctx, ReqContextKey, data)
}

func ReadContext(ctx context.Context) (ReqContextData, bool) {
	data, ok := ctx.Value(ReqContextKey).(ReqContextData)
	return data, ok
}

// extractContext pulls fields from an HTTP request into a ReqContextData,
// returning whatever is available. For GET requests it reads query parameters.
// For POST requests it inspects Content-Type and parses JSON,
// multipart/form-data, or application/x-www-form-urlencoded bodies. The
// request body is always restored before returning. An error is returned only
// for I/O or parse failures, not for missing fields.
func extractContext(r *http.Request) (ReqContextData, error) {

	apiKey := ExtractAPIKey(r)

	if r.Method == http.MethodGet {
		q := r.URL.Query()
		return ReqContextData{
			Model:     q.Get("model"),
			Streaming: q.Get("stream") == "true",
			ApiKey:    apiKey,
		}, nil
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return ReqContextData{}, fmt.Errorf("error reading request body: %w", err)
	}
	defer func() {
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}()

	contentType := r.Header.Get("Content-Type")

	if strings.Contains(contentType, "application/json") {
		return ReqContextData{
			Model:     gjson.GetBytes(bodyBytes, "model").String(),
			Streaming: gjson.GetBytes(bodyBytes, "stream").Bool(),
			ApiKey:    apiKey,
		}, nil
	}

	// Form parsers read from r.Body, so feed them a fresh reader over the
	// buffered bytes. The deferred restore above will reset r.Body again
	// after parsing.
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	if strings.Contains(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return ReqContextData{}, fmt.Errorf("error parsing multipart form: %w", err)
		}
	} else {
		if err := r.ParseForm(); err != nil {
			return ReqContextData{}, fmt.Errorf("error parsing form: %w", err)
		}
	}

	return ReqContextData{
		Model:     r.FormValue("model"),
		Streaming: r.FormValue("stream") == "true",
		ApiKey:    apiKey,
	}, nil
}

// extractAPIKey pulls a candidate API key from the request, preferring Basic,
// then Bearer, then x-api-key.
func ExtractAPIKey(r *http.Request) string {
	var bearerKey, basicKey string
	if auth := r.Header.Get("Authorization"); auth != "" {
		scheme, credentials, ok := strings.Cut(auth, " ")
		if ok {
			switch strings.ToLower(scheme) {
			case "bearer":
				bearerKey = credentials
			case "basic":
				if decoded, err := base64.StdEncoding.DecodeString(credentials); err == nil {
					if parts := strings.SplitN(string(decoded), ":", 2); len(parts) == 2 {
						basicKey = parts[1] // password field is the API key
					}
				}
			}
		}
	}

	switch {
	case basicKey != "":
		return basicKey
	case bearerKey != "":
		return bearerKey
	default:
		return r.Header.Get("x-api-key")
	}
}
