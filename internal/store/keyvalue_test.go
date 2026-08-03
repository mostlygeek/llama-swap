package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestStore_KeyValueSetAndGet(t *testing.T) {
	ctx := context.Background()
	store, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	// An unset key reads back as an empty string.
	value, err := store.GetKeyValue(ctx, KeyInstanceID)
	if err != nil {
		t.Fatalf("GetKeyValue unset: %v", err)
	}
	if value != "" {
		t.Fatalf("unset value = %q, want empty", value)
	}

	if err := store.SetKeyValue(ctx, KeyInstanceID, "abc123"); err != nil {
		t.Fatalf("SetKeyValue: %v", err)
	}
	value, err = store.GetKeyValue(ctx, KeyInstanceID)
	if err != nil {
		t.Fatalf("GetKeyValue: %v", err)
	}
	if value != "abc123" {
		t.Fatalf("value = %q, want abc123", value)
	}

	// Setting an existing key replaces the value instead of failing.
	if err := store.SetKeyValue(ctx, KeyInstanceID, "def456"); err != nil {
		t.Fatalf("SetKeyValue overwrite: %v", err)
	}
	value, err = store.GetKeyValue(ctx, KeyInstanceID)
	if err != nil {
		t.Fatalf("GetKeyValue overwrite: %v", err)
	}
	if value != "def456" {
		t.Fatalf("overwritten value = %q, want def456", value)
	}
}

func TestStore_KeyValueEmptyKey(t *testing.T) {
	ctx := context.Background()
	store, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	if _, err := store.GetKeyValue(ctx, "  "); !errors.Is(err, ErrKeyEmpty) {
		t.Fatalf("GetKeyValue empty key err = %v, want ErrKeyEmpty", err)
	}
	if err := store.SetKeyValue(ctx, "", "value"); !errors.Is(err, ErrKeyEmpty) {
		t.Fatalf("SetKeyValue empty key err = %v, want ErrKeyEmpty", err)
	}
}

func TestStore_KeyValuePersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "llama-swap.sqlite")

	store, err := New(path)
	if err != nil {
		t.Fatalf("New file store: %v", err)
	}
	if err := store.SetKeyValue(ctx, KeyInstanceID, "persisted"); err != nil {
		t.Fatalf("SetKeyValue: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	store, err = New(path)
	if err != nil {
		t.Fatalf("reopen file store: %v", err)
	}
	defer store.Close()

	value, err := store.GetKeyValue(ctx, KeyInstanceID)
	if err != nil {
		t.Fatalf("GetKeyValue: %v", err)
	}
	if value != "persisted" {
		t.Fatalf("value = %q, want persisted", value)
	}
}
