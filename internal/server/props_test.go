package server

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/process"
	"github.com/stretchr/testify/assert"
)

func propsServer(running map[string]process.ProcessState) (*Server, *atomic.Int32) {
	var served atomic.Int32
	local := newStubRouter([]string{"m", "other"}, "props")
	local.running = running
	local.serveHTTP = func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		w.WriteHeader(http.StatusOK)
	}
	return newTestServer(local, newStubRouter(nil, "")), &served
}

func TestServer_PropsAutoloadFalseRefusesModelThatIsNotRunning(t *testing.T) {
	for _, target := range []string{
		"/props?model=m&autoload=false",
		"/props?model=m&autoload=0",
	} {
		for _, state := range []process.ProcessState{process.StateStopping, process.StateStopped} {
			s, served := propsServer(map[string]process.ProcessState{"other": process.StateReady, "m": state})
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))

			assert.Equal(t, http.StatusConflict, w.Code, "%s state=%s", target, state)
			assert.Zero(t, served.Load(), "router must not see the request")
		}
	}
}

func TestServer_PropsAutoloadFalseRefusesModelMissingFromRunning(t *testing.T) {
	s, served := propsServer(map[string]process.ProcessState{"other": process.StateReady})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/props?model=m&autoload=false", nil))

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Zero(t, served.Load())
}

func TestServer_PropsAutoloadFalsePassesRunningModelToRouter(t *testing.T) {
	for _, state := range []process.ProcessState{process.StateReady, process.StateStarting} {
		s, served := propsServer(map[string]process.ProcessState{"m": state})
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/props?model=m&autoload=false", nil))

		assert.Equal(t, http.StatusOK, w.Code, "state=%s", state)
		assert.Equal(t, int32(1), served.Load())
	}
}

func TestServer_PropsWithoutAutoloadFalseReachesRouter(t *testing.T) {
	for _, target := range []string{
		"/props?model=m",
		"/props?model=m&autoload=true",
		"/props?model=m&autoload=1",
		"/props?model=m&autoload=",
	} {
		s, served := propsServer(nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))

		assert.Equal(t, http.StatusOK, w.Code, target)
		assert.Equal(t, int32(1), served.Load(), target)
	}
}

func TestServer_AutoloadIgnoredOutsideProps(t *testing.T) {
	s, served := propsServer(nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sdapi/v1/loras?model=m&autoload=false", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int32(1), served.Load())
}
