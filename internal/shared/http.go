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
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
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
	// Metadata is a request-scoped key/value bag that handlers may mutate
	// while processing. The metrics middleware copies it into ActivityLogEntry.
	Metadata map[string]string
}

var (
	ReqContextKey        = &contextkey{"context"}
	ErrNoModelInContext  = fmt.Errorf("no model in request context")
	ErrNoRouterFound     = fmt.Errorf("no router found for model")
	ErrNoPeerModelFound  = fmt.Errorf("peer model not found")
	ErrNoLocalModelFound = fmt.Errorf("local model not found")
)

func SendError(w http.ResponseWriter, r *http.Request, err error) {
	var httpErr HTTPError
	if errors.As(err, &httpErr) {
		for k, v := range httpErr.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(httpErr.StatusCode())
		w.Write(httpErr.Body())
		return
	}

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

// FetchContext will attempt to get the model id from the context, then
// from an /upstream/<model> path prefix, then from the request body/query.
// If it extracts the model it will store it in the context for downstream
// handlers. An error will be returned when a model cannot be identified.
func FetchContext(r *http.Request, cfg config.Config) (ReqContextData, error) {
	data, ok := ReadContext(r.Context())
	if ok {
		return data, nil
	}

	if strings.HasPrefix(r.URL.Path, "/upstream/") {
		if data, ok := extractUpstreamContext(r, cfg); ok {
			*r = *r.WithContext(SetContext(r.Context(), data))
			return data, nil
		}
		return ReqContextData{}, ErrNoModelInContext
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

// ExtractModel returns the model name encoded in a request without caching
// request data in its context.
func ExtractModel(r *http.Request) (string, error) {
	data, err := extractContext(r)
	return data.Model, err
}

// ReplaceRequestModel replaces model with replacement wherever the request
// encodes its model ID. It returns a request whose cached model context has
// been invalidated so downstream handlers resolve the replacement normally.
func ReplaceRequestModel(r *http.Request, model, replacement string) (*http.Request, error) {
	if strings.HasPrefix(r.URL.Path, "/upstream/") {
		upstreamPath := strings.TrimPrefix(r.PathValue("upstreamPath"), "/")
		if upstreamPath != model && !strings.HasPrefix(upstreamPath, model+"/") {
			return r, nil
		}

		remainingPath := strings.TrimPrefix(upstreamPath, model)
		rewrittenPath := replacement + remainingPath
		if replacement == "" {
			rewrittenPath = ""
		}
		r.SetPathValue("upstreamPath", rewrittenPath)
		r.URL.Path = "/upstream/" + rewrittenPath
		r.URL.RawPath = ""
		return invalidateRequestContext(r), nil
	}

	current, err := ExtractModel(r)
	if err != nil {
		return r, err
	}
	if current != model {
		return r, nil
	}

	if r.Method == http.MethodGet {
		query := r.URL.Query()
		query.Set("model", replacement)
		r.URL.RawQuery = query.Encode()
		return invalidateRequestContext(r), nil
	}

	contentType := r.Header.Get("Content-Type")
	switch {
	case strings.Contains(contentType, "application/json"):
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return r, fmt.Errorf("could not read request body")
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		body, err = sjson.SetBytes(body, "model", replacement)
		if err != nil {
			return r, fmt.Errorf("could not rewrite model in JSON body: %w", err)
		}
		replaceRequestBody(r, body)
	case strings.Contains(contentType, "multipart/form-data"):
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return r, fmt.Errorf("could not parse multipart form: %w", err)
		}
		body, rewrittenContentType, err := replaceMultipartModel(r.MultipartForm, replacement)
		if err != nil {
			return r, err
		}
		r.MultipartForm = nil
		r.Form = nil
		r.PostForm = nil
		r.Header.Set("Content-Type", rewrittenContentType)
		replaceRequestBody(r, body)
	case strings.Contains(contentType, "application/x-www-form-urlencoded"):
		if err := r.ParseForm(); err != nil {
			return r, fmt.Errorf("could not parse form: %w", err)
		}
		r.PostForm.Set("model", replacement)
		replaceRequestBody(r, []byte(r.PostForm.Encode()))
	default:
		if err := r.ParseForm(); err != nil {
			return r, fmt.Errorf("could not parse form: %w", err)
		}
		r.PostForm.Set("model", replacement)
		replaceRequestBody(r, []byte(r.PostForm.Encode()))
	}

	return invalidateRequestContext(r), nil
}

func invalidateRequestContext(r *http.Request) *http.Request {
	if _, ok := ReadContext(r.Context()); !ok {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), ReqContextKey, struct{}{}))
}

func replaceRequestBody(r *http.Request, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.Header.Del("Transfer-Encoding")
	r.Header.Set("Content-Length", strconv.Itoa(len(body)))
	r.ContentLength = int64(len(body))
}

func replaceMultipartModel(form *multipart.Form, replacement string) ([]byte, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	for key, values := range form.Value {
		for _, value := range values {
			if key == "model" {
				value = replacement
			}
			field, err := mw.CreateFormField(key)
			if err != nil {
				return nil, "", fmt.Errorf("error recreating form field %s: %w", key, err)
			}
			if _, err := field.Write([]byte(value)); err != nil {
				return nil, "", fmt.Errorf("error writing form field %s: %w", key, err)
			}
		}
	}

	for key, headers := range form.File {
		for _, fh := range headers {
			part, err := mw.CreateFormFile(key, fh.Filename)
			if err != nil {
				return nil, "", fmt.Errorf("error recreating form file %s: %w", key, err)
			}
			file, err := fh.Open()
			if err != nil {
				return nil, "", fmt.Errorf("error opening uploaded file %s: %w", key, err)
			}
			if _, err := io.Copy(part, file); err != nil {
				file.Close()
				return nil, "", fmt.Errorf("error copying file data %s: %w", key, err)
			}
			file.Close()
		}
	}

	if err := mw.Close(); err != nil {
		return nil, "", fmt.Errorf("error finalizing multipart form: %w", err)
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}

// extractUpstreamContext resolves the model from an /upstream/<model>/... path.
func extractUpstreamContext(r *http.Request, cfg config.Config) (ReqContextData, bool) {
	searchName, realName, _, found := FindModelInPath(cfg, strings.TrimPrefix(r.URL.Path, "/upstream"))
	if !found {
		return ReqContextData{}, false
	}
	return ReqContextData{
		Model:            searchName,
		ModelID:          realName,
		ApiKey:           ExtractAPIKey(r),
		Streaming:        r.URL.Query().Get("stream") == "true",
		SendLoadingState: sendLoadingState(cfg, realName),
		Metadata:         make(map[string]string),
	}, true
}

// sendLoadingState reports whether the configured model wants loading-state SSEs.
func sendLoadingState(cfg config.Config, modelID string) bool {
	if mc, ok := cfg.Models[modelID]; ok {
		return mc.SendLoadingState != nil && *mc.SendLoadingState
	}
	return false
}

// FindModelInPath walks a slash-separated path, building up segments until one
// matches a configured model. This resolves model names that contain slashes
// (e.g. "author/model"). Returns the matched name, its real model ID, the
// remaining path, and whether a match was found.
func FindModelInPath(cfg config.Config, path string) (searchName, realName, remainingPath string, found bool) {
	parts := strings.Split(strings.TrimSpace(path), "/")
	name := ""

	for i, part := range parts {
		if part == "" {
			continue
		}
		if name == "" {
			name = part
		} else {
			name = name + "/" + part
		}

		if modelID, ok := cfg.ResolveBaseModel(name); ok {
			searchName = name
			realName = modelID
			remainingPath = "/" + strings.Join(parts[i+1:], "/")
			found = true
		}
	}

	return
}

func SetContext(ctx context.Context, data ReqContextData) context.Context {
	return context.WithValue(ctx, ReqContextKey, data)
}

func ReadContext(ctx context.Context) (ReqContextData, bool) {
	data, ok := ctx.Value(ReqContextKey).(ReqContextData)
	return data, ok
}

// SetReqData attaches a key/value pair to the request context's metadata map.
// The metadata map must already exist in the context's ReqContextData; callers
// should ensure FetchContext has run or initialize the map themselves.
// It returns an error for nil contexts or contexts without request data.
func SetReqData(ctx context.Context, key, value string) error {
	if ctx == nil {
		return fmt.Errorf("cannot set request metadata on nil context")
	}
	data, ok := ReadContext(ctx)
	if !ok {
		return fmt.Errorf("no request context data found")
	}
	if data.Metadata == nil {
		return fmt.Errorf("no metadata map in request context")
	}
	data.Metadata[key] = value
	return nil
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
			Metadata:  make(map[string]string),
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
			Metadata:  make(map[string]string),
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
		Metadata:  make(map[string]string),
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
