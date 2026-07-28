package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/shared"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// CreateFilterMiddleware returns middleware that applies per-model request-body
// filters to JSON requests before they are forwarded upstream:
//
//   - UseModelName rewrite (issue #69)
//   - StripParams removal (issue #174)
//   - SetParamsByMatch request-field matching (issue #958)
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

			updated, err := shared.ReplaceRequestModel(r, data.Model, useModelName)
			if err != nil {
				shared.SendResponse(w, r, http.StatusBadRequest, err.Error())
				return
			}

			// UseModelName changes only the model name sent upstream. Keep the
			// original request context so routing and metrics still identify
			// the configured model.
			updated = updated.WithContext(r.Context())
			next.ServeHTTP(w, updated)
		})
	}
}

// resolveFilters returns the filter settings for a requested model. UseModelName
// only applies to local models; peers carry filters but no name rewrite.
func resolveFilters(cfg config.Config, requested string) (useModelName string, filters config.Filters, ok bool) {
	if realName, found := cfg.RealModelName(requested); found {
		mc := cfg.Models[realName]
		return mc.UseModelName, mc.Filters.Filters, true
	}
	if peerID, _, found := cfg.ResolvePeerModel(requested); found {
		return "", cfg.Peers[peerID].Filters, true
	}
	return "", config.Filters{}, false
}

// applyFilters rewrites the JSON body in place. Order matches the legacy
// ProxyManager: useModelName, stripParams, setParamsByMatch, setParams, then
// setParamsByID (which can override setParams).
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

	if body, err = applySetParamsByMatch(body, f); err != nil {
		return nil, err
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

// applySetParamsByMatch applies every rule whose key matches its configured
// value. Rules run in configuration order, so a later rule overrides an earlier
// one. Requests that do not carry the key, or carry a different value, are left
// untouched. Because rules match on the request body and never on the model ID,
// a client can change these params between requests without triggering a swap.
func applySetParamsByMatch(body []byte, f config.Filters) ([]byte, error) {
	var err error

	for _, rule := range f.SetParamsByMatch {
		value := gjson.GetBytes(body, rule.Key)
		if !value.Exists() || value.String() != rule.Match {
			continue
		}

		set, keys := rule.SanitizedSet()
		for _, key := range keys {
			if body, err = setMergedParam(body, key, set[key]); err != nil {
				return nil, err
			}
		}
	}

	return body, nil
}

// setMergedParam writes val at key. When the configured value and the value
// already in the request are both objects, sub-keys are written individually so
// keys the client sent survive (e.g. setting chat_template_kwargs.enable_thinking
// keeps any other chat_template_kwargs the client provided). Anything else is a
// plain overwrite.
func setMergedParam(body []byte, key string, val any) ([]byte, error) {
	sub, isMap := val.(map[string]any)
	if !isMap || !gjson.GetBytes(body, key).IsObject() {
		body, err := sjson.SetBytes(body, key, val)
		if err != nil {
			return nil, fmt.Errorf("error setting parameter %s in request", key)
		}
		return body, nil
	}

	subKeys := make([]string, 0, len(sub))
	for subKey := range sub {
		subKeys = append(subKeys, subKey)
	}
	sort.Strings(subKeys)

	var err error
	for _, subKey := range subKeys {
		path := key + "." + escapePathSegment(subKey)
		if body, err = sjson.SetBytes(body, path, sub[subKey]); err != nil {
			return nil, fmt.Errorf("error setting parameter %s in request", path)
		}
	}
	return body, nil
}

// escapePathSegment escapes the characters gjson/sjson treat as path syntax so
// a sub-key is matched literally when joined into a composite path.
func escapePathSegment(segment string) string {
	var b strings.Builder
	for _, r := range segment {
		switch r {
		case '.', '*', '?', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
