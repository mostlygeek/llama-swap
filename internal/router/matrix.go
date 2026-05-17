package router

import (
	"net/http"
	"time"
)

type MatrixRouter struct {
	// TODO: implement
}

func (r *MatrixRouter) Shutdown(timeout time.Duration) error {
	// TODO: implement
	return nil
}

func (r *MatrixRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// TODO: implement
}
