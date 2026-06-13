// Package chain composes http.Handler middleware into a single handler.
//
// A Middleware wraps a downstream http.Handler and may run logic before or
// after delegating to it, or short-circuit by not calling next at all
// (e.g. auth failure, CORS preflight).
package chain

import "net/http"

// Middleware wraps an http.Handler with cross-cutting behavior. It receives
// the next handler in the chain and returns a handler that may call next,
// modify the request/response around it, or short-circuit.
type Middleware func(next http.Handler) http.Handler

// Chain is a reusable middleware stack. Build it once with New (and optionally
// extend per-route with Append), then call Then to wrap each terminal handler
// when registering routes against an http.ServeMux:
//
//	api := chain.New(authMW, corsMW)
//	mux.Handle("/v1/chat/completions", api.Then(dispatch))
//	mux.Handle("/v1/embeddings",       api.Append(filters).Then(dispatch))
//
// Middlewares execute left-to-right: mws[0] runs first and may call into
// mws[1], and so on, with the terminal handler invoked last. A middleware
// that does not call next short-circuits the remainder of the chain.
// A zero Chain is valid and applies no middleware.
type Chain struct {
	mws []Middleware
}

// New returns a Chain that applies mws left-to-right around any terminal
// handler passed to Then.
func New(mws ...Middleware) Chain {
	cp := make([]Middleware, len(mws))
	copy(cp, mws)
	return Chain{mws: cp}
}

// Append returns a new Chain with mws added after the existing middleware.
// The receiver is not modified, so a base Chain can be safely reused across
// multiple routes that each need different per-route additions.
func (c Chain) Append(mws ...Middleware) Chain {
	out := make([]Middleware, 0, len(c.mws)+len(mws))
	out = append(out, c.mws...)
	out = append(out, mws...)
	return Chain{mws: out}
}

// Then wraps final with the chain's middleware and returns the resulting
// handler, suitable for passing to http.ServeMux.Handle. With an empty chain,
// Then returns final unchanged.
func (c Chain) Then(final http.Handler) http.Handler {
	h := final
	for i := len(c.mws) - 1; i >= 0; i-- {
		h = c.mws[i](h)
	}
	return h
}

// ThenFunc is shorthand for Then(http.HandlerFunc(f)).
func (c Chain) ThenFunc(f http.HandlerFunc) http.Handler {
	return c.Then(f)
}
