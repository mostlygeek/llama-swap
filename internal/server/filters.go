package server

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/router"
	"github.com/mostlygeek/llama-swap/internal/shared"
	"github.com/tidwall/sjson"
)

// CreateFilterMiddleware returns middleware that applies per-model request-body
// filters to JSON requests before they are forwarded upstream:
//
//   - UseModelName rewrite (issue #69)
//   - StripParams removal (issue #174)
//   - SetParams injection (issue #453)
//   - SetParamsByID per-alias overrides
//
// Non-JSON requests (GET, multipart forms) pass through untouched. The buffered
// body is re-attached with Content-Length / Transfer-Encoding cleanup so the
// downstream reverse proxy forwards the correct bytes (see issue #11).
func CreateFilterMiddleware(cfg config.Config) chain.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
				next.ServeHTTP(w, r)
				return
			}

			data, err := shared.FetchContext(r, cfg)
			if err != nil {
				shared.SendError(w, r, shared.ErrNoModelInContext)
				return
			}

			useModelName, filters, ok := resolveFilters(cfg, data.Model)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			body, err := io.ReadAll(r.Body)
			if err != nil {
				shared.SendResponse(w, r, http.StatusBadRequest, "could not read request body")
				return
			}

			body, err = applyFilters(body, data.Model, useModelName, filters)
			if err != nil {
				shared.SendResponse(w, r, http.StatusInternalServerError, err.Error())
				return
			}

			r.Body = io.NopCloser(bytes.NewReader(body))
			r.Header.Del("Transfer-Encoding")
			r.Header.Set("Content-Length", strconv.Itoa(len(body)))
			r.ContentLength = int64(len(body))

			next.ServeHTTP(w, r)
		})
	}
}

// CreateFormFilterMiddleware returns middleware that applies the UseModelName
// rewrite (issue #69) to multipart/form-data requests before they are forwarded
// upstream. JSON-body filters (StripParams, SetParams) do not apply to form
// endpoints; only the "model" field is rewritten.
//
// Non-multipart requests pass through untouched. When a rewrite is needed the
// form is reconstructed and re-attached with Content-Type / Content-Length
// cleanup so the downstream reverse proxy forwards the correct bytes.
func CreateFormFilterMiddleware(cfg config.Config) chain.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
				next.ServeHTTP(w, r)
				return
			}

			data, err := shared.FetchContext(r, cfg)
			if err != nil {
				shared.SendError(w, r, shared.ErrNoModelInContext)
				return
			}

			useModelName, _, ok := resolveFilters(cfg, data.Model)
			if !ok || useModelName == "" {
				next.ServeHTTP(w, r)
				return
			}

			if err := r.ParseMultipartForm(32 << 20); err != nil {
				shared.SendResponse(w, r, http.StatusBadRequest, fmt.Sprintf("error parsing multipart form: %s", err.Error()))
				return
			}

			body, contentType, err := rewriteMultipartModel(r.MultipartForm, useModelName)
			if err != nil {
				shared.SendResponse(w, r, http.StatusInternalServerError, err.Error())
				return
			}

			r.Body = io.NopCloser(bytes.NewReader(body))
			r.MultipartForm = nil
			r.Header.Del("Transfer-Encoding")
			r.Header.Set("Content-Type", contentType)
			r.Header.Set("Content-Length", strconv.Itoa(len(body)))
			r.ContentLength = int64(len(body))

			next.ServeHTTP(w, r)
		})
	}
}

// rewriteMultipartModel reconstructs a multipart form, replacing the "model"
// field value with useModelName. It returns the encoded body and the matching
// Content-Type header (which carries the generated boundary).
func rewriteMultipartModel(form *multipart.Form, useModelName string) ([]byte, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	for key, values := range form.Value {
		for _, value := range values {
			if key == "model" {
				value = useModelName
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

// resolveFilters returns the filter settings for a requested model. UseModelName
// rewrites the model field for local models and peer virtual sub-IDs.
func resolveFilters(cfg config.Config, requested string) (useModelName string, filters config.Filters, ok bool) {
	if realName, found := cfg.RealModelName(requested); found {
		mc := cfg.Models[realName]
		return mc.UseModelName, mc.Filters.Filters, true
	}
	peerIDs := make([]string, 0, len(cfg.Peers))
	for peerID := range cfg.Peers {
		peerIDs = append(peerIDs, peerID)
	}
	sort.Strings(peerIDs)

	for _, peerID := range peerIDs {
		peer := cfg.Peers[peerID]
		for _, m := range peer.Models {
			if m == requested {
				return "", peer.Filters, true
			}
		}
	}
	// Also check setParamsByID keys for virtual sub-ID requests
	if p, baseModel, found := router.ResolveVirtualSubID(cfg, requested); found {
		return baseModel, p.Filters, true
	}
	return "", config.Filters{}, false
}

// applyFilters rewrites the JSON body in place. Order matches the legacy
// ProxyManager: useModelName, stripParams, setParams, then setParamsByID (which
// can override setParams).
func applyFilters(body []byte, requested, useModelName string, f config.Filters) ([]byte, error) {
	var err error

	if useModelName != "" {
		if body, err = sjson.SetBytes(body, "model", useModelName); err != nil {
			return nil, fmt.Errorf("error rewriting model name in JSON: %w", err)
		}
	}

	for _, param := range f.SanitizedStripParams() {
		if body, err = sjson.DeleteBytes(body, param); err != nil {
			return nil, fmt.Errorf("error stripping parameter %s from request", param)
		}
	}

	setParams, setKeys := f.SanitizedSetParams()
	for _, key := range setKeys {
		if body, err = sjson.SetBytes(body, key, setParams[key]); err != nil {
			return nil, fmt.Errorf("error setting parameter %s in request", key)
		}
	}

	byID, byIDKeys := f.SanitizedSetParamsByID(requested)
	for _, key := range byIDKeys {
		if body, err = sjson.SetBytes(body, key, byID[key]); err != nil {
			return nil, fmt.Errorf("error setting parameter %s in request", key)
		}
	}

	return body, nil
}
