package chain

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recordingMiddleware appends tag before calling next and "-after-"+tag after.
func recordingMiddleware(tag string, log *[]string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*log = append(*log, tag)
			next.ServeHTTP(w, r)
			*log = append(*log, "after-"+tag)
		})
	}
}

func TestChain_HandlersExecuteInDeclaredOrder(t *testing.T) {
	var log []string
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log = append(log, "final")
	})

	h := New(
		recordingMiddleware("a", &log),
		recordingMiddleware("b", &log),
		recordingMiddleware("c", &log),
	).Then(final)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	want := []string{"a", "b", "c", "final", "after-c", "after-b", "after-a"}
	if !equal(log, want) {
		t.Fatalf("execution order mismatch:\n got: %v\nwant: %v", log, want)
	}
}

func TestChain_ShortCircuitsWhenMiddlewareDoesNotCallNext(t *testing.T) {
	var log []string
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log = append(log, "final")
	})

	gate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log = append(log, "gate")
			w.WriteHeader(http.StatusUnauthorized)
		})
	}

	h := New(
		recordingMiddleware("outer", &log),
		gate,
		recordingMiddleware("inner", &log),
	).Then(final)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	want := []string{"outer", "gate", "after-outer"}
	if !equal(log, want) {
		t.Fatalf("short-circuit order mismatch:\n got: %v\nwant: %v", log, want)
	}
}

func TestChain_EarlyWritesAreVisibleToLaterMiddleware(t *testing.T) {
	header := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Set-By", "outer")
			_, _ = io.WriteString(w, "outer:")
			next.ServeHTTP(w, r)
		})
	}
	inner := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The outer middleware already set the header; we should see it.
			if got := w.Header().Get("X-Set-By"); got != "outer" {
				_, _ = io.WriteString(w, "missing-header;")
			}
			_, _ = io.WriteString(w, "inner:")
			next.ServeHTTP(w, r)
		})
	}
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "final")
	})

	h := New(header, inner).Then(final)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body, _ := io.ReadAll(rec.Body)
	if got := string(body); !strings.Contains(got, "outer:inner:final") {
		t.Fatalf("body: got %q, want it to contain %q", got, "outer:inner:final")
	}
	if got := rec.Header().Get("X-Set-By"); got != "outer" {
		t.Fatalf("header X-Set-By: got %q, want %q", got, "outer")
	}
}

func TestChain_ReusableAcrossRoutesViaThen(t *testing.T) {
	var log []string
	base := New(
		recordingMiddleware("auth", &log),
		recordingMiddleware("cors", &log),
	)

	mux := http.NewServeMux()
	mux.Handle("/a", base.ThenFunc(func(w http.ResponseWriter, r *http.Request) {
		log = append(log, "handler-a")
	}))
	mux.Handle("/b", base.ThenFunc(func(w http.ResponseWriter, r *http.Request) {
		log = append(log, "handler-b")
	}))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, path := range []string{"/a", "/b"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
	}

	want := []string{
		"auth", "cors", "handler-a", "after-cors", "after-auth",
		"auth", "cors", "handler-b", "after-cors", "after-auth",
	}
	if !equal(log, want) {
		t.Fatalf("reusable chain order mismatch:\n got: %v\nwant: %v", log, want)
	}
}

func TestChain_AppendDoesNotMutateReceiver(t *testing.T) {
	var log []string
	base := New(recordingMiddleware("base", &log))
	extended := base.Append(recordingMiddleware("extra", &log))

	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log = append(log, "final")
	})

	// Run extended first to surface any aliasing of the underlying slice.
	rec := httptest.NewRecorder()
	extended.Then(final).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	rec = httptest.NewRecorder()
	base.Then(final).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{
		"base", "extra", "final", "after-extra", "after-base",
		"base", "final", "after-base",
	}
	if !equal(log, want) {
		t.Fatalf("Append must not mutate the receiver:\n got: %v\nwant: %v", log, want)
	}
}

func TestChain_ZeroValueAndEmptyThenAreIdentity(t *testing.T) {
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	for name, c := range map[string]Chain{
		"zero":  {},
		"empty": New(),
	} {
		t.Run(name, func(t *testing.T) {
			h := c.Then(final)
			if _, ok := h.(http.HandlerFunc); !ok {
				t.Fatalf("expected http.HandlerFunc identity, got %T", h)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if rec.Code != http.StatusTeapot {
				t.Fatalf("status: got %d, want %d", rec.Code, http.StatusTeapot)
			}
		})
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
