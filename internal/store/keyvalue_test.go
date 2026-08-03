package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// registerTestKey registers key for the duration of the test.
func registerTestKey(t *testing.T, key Key, public bool) {
	t.Helper()
	t.Cleanup(RegisterKVKeyForTest(key, public))
}

type kvTestSettings struct {
	Theme   string `json:"theme"`
	Columns []int  `json:"columns"`
}

func TestStore_KeyValueSetGet(t *testing.T) {
	const settingsKey Key = "test.settings"
	const countKey Key = "test.count"
	registerTestKey(t, settingsKey, true)
	registerTestKey(t, countKey, false)

	ctx := context.Background()
	store, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	want := kvTestSettings{Theme: "dark", Columns: []int{1, 2, 3}}
	if err := SetKV(ctx, store, settingsKey, want); err != nil {
		t.Fatalf("SetKV: %v", err)
	}
	got, found, err := GetKV[kvTestSettings](ctx, store, settingsKey)
	if err != nil {
		t.Fatalf("GetKV: %v", err)
	}
	if !found {
		t.Fatalf("found = false, want true")
	}
	if got.Theme != want.Theme || len(got.Columns) != len(want.Columns) || got.Columns[2] != 3 {
		t.Fatalf("got = %+v, want %+v", got, want)
	}

	// A second Set replaces the stored value.
	if err := SetKV(ctx, store, countKey, 7); err != nil {
		t.Fatalf("SetKV: %v", err)
	}
	if err := SetKV(ctx, store, countKey, 42); err != nil {
		t.Fatalf("SetKV: %v", err)
	}
	count, found, err := GetKV[int](ctx, store, countKey)
	if err != nil {
		t.Fatalf("GetKV: %v", err)
	}
	if !found || count != 42 {
		t.Fatalf("count = %d, found = %v", count, found)
	}
}

func TestStore_KeyValueMissing(t *testing.T) {
	const key Key = "test.missing"
	registerTestKey(t, key, true)

	ctx := context.Background()
	store, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	got, found, err := GetKV[kvTestSettings](ctx, store, key)
	if err != nil {
		t.Fatalf("GetKV: %v", err)
	}
	if found {
		t.Fatalf("found = true, want false")
	}
	if got.Theme != "" || got.Columns != nil {
		t.Fatalf("got = %+v, want zero value", got)
	}
}

func TestStore_KeyValueUnknownKey(t *testing.T) {
	ctx := context.Background()
	store, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	if err := SetKV(ctx, store, Key("test.unregistered"), 1); err == nil {
		t.Fatalf("SetKV err = nil, want unknown key error")
	} else if !strings.Contains(err.Error(), "unknown keyvalue key") {
		t.Fatalf("SetKV err = %v", err)
	}

	if _, _, err := GetKV[int](ctx, store, Key("test.unregistered")); err == nil {
		t.Fatalf("GetKV err = nil, want unknown key error")
	} else if !strings.Contains(err.Error(), "unknown keyvalue key") {
		t.Fatalf("GetKV err = %v", err)
	}
}

func TestStore_KeyValueTypeMismatch(t *testing.T) {
	const key Key = "test.mismatch"
	registerTestKey(t, key, true)

	ctx := context.Background()
	store, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	if err := SetKV(ctx, store, key, "a string"); err != nil {
		t.Fatalf("SetKV: %v", err)
	}
	if _, _, err := GetKV[int](ctx, store, key); err == nil {
		t.Fatalf("GetKV err = nil, want unmarshal error")
	}
}

func TestStore_KeyValuePublicKey(t *testing.T) {
	const publicKey Key = "test.public"
	const privateKey Key = "test.private"
	registerTestKey(t, publicKey, true)
	registerTestKey(t, privateKey, false)

	if got, ok := PublicKVKey(string(publicKey)); !ok || got != publicKey {
		t.Fatalf("PublicKVKey(%q) = %q, %v", publicKey, got, ok)
	}
	if got, ok := PublicKVKey(string(privateKey)); ok {
		t.Fatalf("PublicKVKey(%q) = %q, %v, want not ok", privateKey, got, ok)
	}
	if got, ok := PublicKVKey("test.nope"); ok {
		t.Fatalf("PublicKVKey(unknown) = %q, %v, want not ok", got, ok)
	}
}

func TestStore_KeyValuePersists(t *testing.T) {
	const key Key = "test.persisted"
	registerTestKey(t, key, true)

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "llama-swap.sqlite")

	store, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := SetKV(ctx, store, key, kvTestSettings{Theme: "light"}); err != nil {
		t.Fatalf("SetKV: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := New(path)
	if err != nil {
		t.Fatalf("New reopen: %v", err)
	}
	defer reopened.Close()

	got, found, err := GetKV[kvTestSettings](ctx, reopened, key)
	if err != nil {
		t.Fatalf("GetKV: %v", err)
	}
	if !found || got.Theme != "light" {
		t.Fatalf("got = %+v, found = %v", got, found)
	}
}
