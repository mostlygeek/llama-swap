// Package hint lets layers lower in the request chain tell layers higher up
// what they learned: where a request was routed and what kind of server
// answers it. See README.md.
package hint

import "context"

// Route says which side of llama-swap served a request.
type Route string

const (
	RouteNone  Route = ""
	RouteLocal Route = "local"
	RoutePeer  Route = "peer"
)

// Request holds what the layers below the metrics middleware learn while
// dispatching a request. It only carries facts the layers above cannot work
// out themselves; the model ID and path are already known to them.
//
// Only the request goroutine writes it, and only before the handler returns,
// so the reader (after next.ServeHTTP returns) needs no lock.
type Request struct {
	Route    Route
	PeerID   string   // set when Route == RoutePeer
	Upstream Upstream // Unknown when the layer does not know
}

type contextKey struct{}

// New returns a context carrying a new, empty Request, and the Request.
func New(ctx context.Context) (context.Context, *Request) {
	h := &Request{}
	return context.WithValue(ctx, contextKey{}, h), h
}

// From returns the Request in ctx, or nil when there is none.
func From(ctx context.Context) *Request {
	h, _ := ctx.Value(contextKey{}).(*Request)
	return h
}
