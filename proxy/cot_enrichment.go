package proxy

import (
	"context"
	"net/http"
)

type cotContextKey struct{}

func withCoTEnrichment(ctx context.Context, enabled bool) context.Context {
	if !enabled {
		return ctx
	}
	return context.WithValue(ctx, cotContextKey{}, true)
}

func isCoTEnrichment(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	val, ok := ctx.Value(cotContextKey{}).(bool)
	return ok && val
}

type cotRoundTripper struct {
	base    http.RoundTripper
	process *Process
}

func (rt *cotRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	transport := rt.base
	if transport == nil {
		transport = http.DefaultTransport
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	if rt.process == nil || !rt.process.shouldInjectCoT(req.Context(), resp) {
		return resp, nil
	}

	resp.Header.Del("Content-Length")
	resp.Body = rt.process.wrapCoTStream(resp.Body)
	return resp, nil
}
