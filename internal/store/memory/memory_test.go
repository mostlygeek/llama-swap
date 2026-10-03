package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/store"
)

func TestMemory_PerCaptureLimit(t *testing.T) {
	ctx := context.Background()
	repo := New(100_000, 1_000)

	if err := repo.Put(ctx, 1, blobOf(900)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	err := repo.Put(ctx, 2, blobOf(1_001))
	if !errors.Is(err, store.ErrCaptureTooLarge) {
		t.Fatalf("Put(1001) = %v, want ErrCaptureTooLarge", err)
	}

	// The rejected write must not have evicted the stored capture.
	if _, found, err := repo.Get(ctx, 1); err != nil || !found {
		t.Fatal("a rejected capture must not evict stored ones")
	}
}

// maxItemBytes 0 means "no per-capture limit", which is how the legacy
// captureBuffer path is constructed: it has never had a per-capture setting.
func TestMemory_NoPerCaptureLimitWhenZero(t *testing.T) {
	ctx := context.Background()
	repo := New(100_000, 0)

	if err := repo.Put(ctx, 1, blobOf(99_000)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, found, err := repo.Get(ctx, 1); err != nil || !found {
		t.Fatal("capture not stored")
	}
}

// A blob larger than the whole budget is the same failure for the caller:
// ErrCaptureTooLarge, nothing stored, nothing evicted.
func TestMemory_BlobLargerThanBudgetIsRejected(t *testing.T) {
	ctx := context.Background()
	repo := New(10_000, 0)

	if err := repo.Put(ctx, 1, blobOf(5_000)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	err := repo.Put(ctx, 2, blobOf(20_000))
	if !errors.Is(err, store.ErrCaptureTooLarge) {
		t.Fatalf("Put(20000) = %v, want ErrCaptureTooLarge", err)
	}
	if _, found, _ := repo.Get(ctx, 2); found {
		t.Fatal("an oversized capture must not be stored")
	}
}

// Eviction is FIFO by insertion order, so a capture re-written under an old id
// moves to the end of the queue. The sqlite backend keys by activity id, where
// the same write leaves the row oldest. The proxy writes each activity id once,
// so the two agree in practice; this pins the difference down.
func TestMemory_ReplacedCaptureBecomesNewest(t *testing.T) {
	ctx := context.Background()
	repo := New(30_000, 0)

	for _, id := range []int{1, 2, 3} {
		if err := repo.Put(ctx, id, blobOf(10_000)); err != nil {
			t.Fatalf("Put(%d): %v", id, err)
		}
	}

	// id 1 is re-written, so it becomes the newest; id 2 is now the oldest.
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
