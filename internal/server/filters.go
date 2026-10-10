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
	"github.com/mostlygeek/llama-swap/internal/spl"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// CreateFilterMiddleware returns middleware that applies per-model request-body
// filters to JSON requests before they are forwarded upstream:
//
//   - UseModelName rewrite (issue #69)
//   - StripParams removal (issue #174)
//   - SetParams injection (issue #453)
//   - SetParamsByID per-alias overrides
//   - SPL programs: hooks.on_request, then the model or peer filters.policy
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

			data, err := swaputil.FetchContext(r, cfg)
			if err != nil {
				swaputil.SendError(w, r, swaputil.ErrNoModelInContext)
				return
			}

			useModelName, filters, ok := resolveFilters(cfg, data.Model)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			body, err := io.ReadAll(r.Body)
			if err != nil {
				swaputil.SendResponse(w, r, http.StatusBadRequest, "could not read request body")
				return
			}

			body, err = applyFilters(body, data.Model, useModelName, filters)
			if err != nil {
				swaputil.SendResponse(w, r, http.StatusInternalServerError, err.Error())
				return
			}

			if programs := cfg.SPL(); programs != nil {
				hook, policy := resolvePolicies(cfg, programs, data.Model)
				if hook != nil || policy != nil {
					if data.Metadata == nil {
						// extractContext allocates the map on the normal path;
						// this covers hand-built contexts so context.* writes
						// still reach the metrics middleware.
						data.Metadata = make(map[string]string)
						*r = *r.WithContext(swaputil.SetContext(r.Context(), data))
					}
					req := &spl.Request{
						Body:    body,
						Context: data.Metadata,
						APIKey:  data.ApiKey,
						Model:   data.Model,
						Path:    r.URL.Path,
						Method:  r.Method,
						Header:  r.Header,
					}
					denied, err := runPolicies(programs.Library, req, hook, policy)
					if err != nil {
						swaputil.SendResponse(w, r, http.StatusInternalServerError, err.Error())
						return
					}
					if denied != nil {
						swaputil.SendResponse(w, r, denied.Status, denied.Message)
						return
					}
					body = req.Body
				}
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

			data, err := swaputil.FetchContext(r, cfg)
			if err != nil {
				swaputil.SendError(w, r, swaputil.ErrNoModelInContext)
				return
			}

			useModelName, _, ok := resolveFilters(cfg, data.Model)
			if !ok || useModelName == "" {
				next.ServeHTTP(w, r)
				return
			}

			updated, err := swaputil.ReplaceRequestModel(r, data.Model, useModelName)
			if err != nil {
				swaputil.SendResponse(w, r, http.StatusBadRequest, err.Error())
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

// resolvePolicies returns the global hooks.on_request program and the
// filters.policy of the requested model or peer. The lookup order matches
// resolveFilters: local models first, then peers.
func resolvePolicies(cfg config.Config, programs *config.SPLPrograms, requested string) (hook, policy *spl.Program) {
	hook = programs.OnRequest
	if realName, found := cfg.RealModelName(requested); found {
		return hook, programs.Models[realName]
	}
	if peerID, _, found := cfg.ResolvePeerModel(requested); found {
		return hook, programs.Peers[peerID]
	}
	return hook, nil
}

// runPolicies runs each non-nil program in order over req. The body and
// context carry from one program to the next. The first deny stops the chain.
func runPolicies(lib *spl.Library, req *spl.Request, programs ...*spl.Program) (*spl.Denial, error) {
	for _, prog := range programs {
		if prog == nil {
			continue
		}
		denied, err := prog.Run(lib, req)
		if err != nil {
			return nil, fmt.Errorf("policy error: %w", err)
		}
		if denied != nil {
			return denied, nil
		}
	}
	return nil, nil
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

	setParams, setKeys, setSoft := f.SanitizedSetParams()
	byID, byIDKeys, byIDSoft := f.SanitizedSetParamsByID(requested)

	// Set-if-undefined keys ("key?", issue #1052) only fill parameters the body
	// does not carry at that point in the pipeline. Filters apply like a pipe —
	// stripParams | setParams | setParamsByID — so a stripped key counts as
	// undefined, and a key an earlier stage set counts as defined (a "key?" in
	// setParamsByID is a no-op when setParams already set it).
	for _, key := range setKeys {
		if setSoft[key] && gjson.GetBytes(body, key).Exists() {
			continue
		}
		if body, err = sjson.SetBytes(body, key, setParams[key]); err != nil {
			return nil, fmt.Errorf("error setting parameter %s in request", key)
		}
	}

	for _, key := range byIDKeys {
		if byIDSoft[key] && gjson.GetBytes(body, key).Exists() {
			continue
		}
		if body, err = sjson.SetBytes(body, key, byID[key]); err != nil {
			return nil, fmt.Errorf("error setting parameter %s in request", key)
		}
	}

	return body, nil
}
