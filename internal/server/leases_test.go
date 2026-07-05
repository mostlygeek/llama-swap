package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/router"
)

func doJSON(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestLeaseAPI_Acquire(t *testing.T) {
	local := newStubRouter([]string{"m1"}, "")
	var gotModel, gotHolder, gotReason string
	var gotTTL time.Duration
	local.acquireLease = func(model, holder, reason string, ttl time.Duration) (router.Lease, error) {
		gotModel, gotHolder, gotReason, gotTTL = model, holder, reason, ttl
		return router.Lease{ID: "L1", Model: model, Holder: holder, Reason: reason, State: router.LeaseActive}, nil
	}
	s := newTestServer(local, newStubRouter(nil, ""))

	w := doJSON(t, s, http.MethodPost, "/leases", `{"model":"m1","holder":"run.py","reason":"batch","ttl":"2h"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if gotModel != "m1" || gotHolder != "run.py" || gotReason != "batch" || gotTTL != 2*time.Hour {
		t.Fatalf("handler passed model=%q holder=%q reason=%q ttl=%v", gotModel, gotHolder, gotReason, gotTTL)
	}
	var lease router.Lease
	if err := json.Unmarshal(w.Body.Bytes(), &lease); err != nil || lease.ID != "L1" {
		t.Fatalf("bad lease body: %v %q", err, w.Body.String())
	}
}

func TestLeaseAPI_AcquireValidatesBody(t *testing.T) {
	s := newTestServer(newStubRouter([]string{"m1"}, ""), newStubRouter(nil, ""))

	if w := doJSON(t, s, http.MethodPost, "/leases", `not json`); w.Code != http.StatusBadRequest {
		t.Errorf("invalid json: status=%d want 400", w.Code)
	}
	if w := doJSON(t, s, http.MethodPost, "/leases", `{"holder":"x"}`); w.Code != http.StatusBadRequest {
		t.Errorf("missing model: status=%d want 400", w.Code)
	}
	if w := doJSON(t, s, http.MethodPost, "/leases", `{"model":"m1","ttl":"banana"}`); w.Code != http.StatusBadRequest {
		t.Errorf("bad ttl: status=%d want 400", w.Code)
	}
}

func TestLeaseAPI_AcquireUnknownModel(t *testing.T) {
	local := newStubRouter([]string{"m1"}, "")
	local.acquireLease = func(_, _, _ string, _ time.Duration) (router.Lease, error) {
		return router.Lease{}, router.ErrUnknownModel
	}
	s := newTestServer(local, newStubRouter(nil, ""))

	w := doJSON(t, s, http.MethodPost, "/leases", `{"model":"ghost"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", w.Code)
	}
}

func TestLeaseAPI_Release(t *testing.T) {
	local := newStubRouter(nil, "")
	var gotID string
	local.releaseLease = func(id string) bool { gotID = id; return true }
	s := newTestServer(local, newStubRouter(nil, ""))

	w := doJSON(t, s, http.MethodDelete, "/leases/ABC", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", w.Code)
	}
	if gotID != "ABC" {
		t.Fatalf("release id=%q want ABC", gotID)
	}

	local.releaseLease = func(string) bool { return false }
	if w := doJSON(t, s, http.MethodDelete, "/leases/GONE", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown id: status=%d want 404", w.Code)
	}
}

func TestLeaseAPI_Extend(t *testing.T) {
	local := newStubRouter(nil, "")
	var gotID string
	var gotTTL time.Duration
	local.extendLease = func(id string, ttl time.Duration) (router.Lease, bool) {
		gotID, gotTTL = id, ttl
		return router.Lease{ID: id, State: router.LeaseActive}, true
	}
	s := newTestServer(local, newStubRouter(nil, ""))

	w := doJSON(t, s, http.MethodPost, "/leases/XYZ/extend", `{"ttl":"1h"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if gotID != "XYZ" || gotTTL != time.Hour {
		t.Fatalf("extend id=%q ttl=%v", gotID, gotTTL)
	}

	local.extendLease = func(string, time.Duration) (router.Lease, bool) { return router.Lease{}, false }
	if w := doJSON(t, s, http.MethodPost, "/leases/GONE/extend", `{"ttl":"1h"}`); w.Code != http.StatusNotFound {
		t.Fatalf("expired: status=%d want 404", w.Code)
	}
}

func TestLeaseAPI_Kill_RequiresExactlyOneSelector(t *testing.T) {
	local := newStubRouter(nil, "")
	local.killLeases = func(id, model, holder string) []router.Lease {
		return []router.Lease{{ID: "L1", Model: model}}
	}
	s := newTestServer(local, newStubRouter(nil, ""))

	if w := doJSON(t, s, http.MethodPost, "/leases/kill", `{}`); w.Code != http.StatusBadRequest {
		t.Errorf("no selector: status=%d want 400", w.Code)
	}
	if w := doJSON(t, s, http.MethodPost, "/leases/kill", `{"id":"a","model":"b"}`); w.Code != http.StatusBadRequest {
		t.Errorf("two selectors: status=%d want 400", w.Code)
	}
	w := doJSON(t, s, http.MethodPost, "/leases/kill", `{"model":"m1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("one selector: status=%d want 200", w.Code)
	}
	var resp struct {
		Killed []router.Lease `json:"killed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || len(resp.Killed) != 1 {
		t.Fatalf("kill body=%q err=%v", w.Body.String(), err)
	}
}

func TestLeaseAPI_List(t *testing.T) {
	local := newStubRouter(nil, "")
	local.listLeases = func() []router.LeaseView {
		return []router.LeaseView{{Lease: router.Lease{ID: "L1", Model: "m"}, ActiveRequests: 2, TTLRemainingMS: 1000}}
	}
	s := newTestServer(local, newStubRouter(nil, ""))

	w := doJSON(t, s, http.MethodGet, "/leases", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var resp struct {
		Leases []router.LeaseView `json:"leases"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || len(resp.Leases) != 1 || resp.Leases[0].ActiveRequests != 2 {
		t.Fatalf("list body=%q err=%v", w.Body.String(), err)
	}
}

func TestLeaseAPI_CanLoad(t *testing.T) {
	local := newStubRouter([]string{"m1"}, "")
	local.canLoad = func(model string) (router.LoadVerdict, error) {
		return router.LoadVerdict{
			Model:      model,
			WouldEvict: []string{"other"},
			Blocked:    true,
			BlockedBy:  []router.Blocker{{Model: "other", Holder: "h", Reason: "r"}},
		}, nil
	}
	s := newTestServer(local, newStubRouter(nil, ""))

	w := doJSON(t, s, http.MethodGet, "/leases/can-load/m1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	var v router.LoadVerdict
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || !v.Blocked || len(v.BlockedBy) != 1 {
		t.Fatalf("verdict body=%q err=%v", w.Body.String(), err)
	}
}
