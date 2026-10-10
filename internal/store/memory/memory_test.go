package memory

import (
	"context"
	"testing"
)

// Eviction is FIFO by insertion order, so a re-written id becomes the
// newest.
func TestMemory_ReplacedCaptureBecomesNewest(t *testing.T) {
	ctx := context.Background()
	repo := New(30_000)

	for _, id := range []int{1, 2, 3} {
		if err := repo.Put(ctx, id, blobOf(10_000)); err != nil {
			t.Fatalf("Put(%d): %v", id, err)
		}
	}

	if err := repo.Put(ctx, 1, blobOf(10_000)); err != nil {
		t.Fatalf("Put(1): %v", err)
	}
	if err := repo.Put(ctx, 4, blobOf(10_000)); err != nil {
		t.Fatalf("Put(4): %v", err)
	}

	found, err := repo.Has(ctx, []int{1, 2, 3, 4})
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	if !found[1] || !found[4] {
		t.Fatalf("found = %v, want the re-written capture and the newest one", found)
	}
	if found[2] {
		t.Fatal("the re-written capture must not have kept id 2 as the oldest")
	}
}

func blobOf(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}
