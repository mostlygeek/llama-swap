package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/chain"
	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/router"
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

			requested, _, err := router.FetchModel(r, cfg)
			if err != nil {
				// Let the dispatcher report the missing-model error.
				next.ServeHTTP(w, r)
				return
			}

			useModelName, filters, ok := resolveFilters(cfg, requested)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			body, err := io.ReadAll(r.Body)
			if err != nil {
				router.SendResponse(w, r, http.StatusBadRequest, "could not read request body")
				return
			}

			body, err = applyFilters(body, requested, useModelName, filters)
			if err != nil {
				router.SendResponse(w, r, http.StatusInternalServerError, err.Error())
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

// resolveFilters returns the filter settings for a requested model. UseModelName
// only applies to local models; peers carry filters but no name rewrite.
func resolveFilters(cfg config.Config, requested string) (useModelName string, filters config.Filters, ok bool) {
	if realName, found := cfg.RealModelName(requested); found {
		mc := cfg.Models[realName]
		return mc.UseModelName, mc.Filters.Filters, true
	}
	for _, peer := range cfg.Peers {
		for _, m := range peer.Models {
			if m == requested {
				return "", peer.Filters, true
			}
		}
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
