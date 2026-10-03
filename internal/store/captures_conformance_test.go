package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mostlygeek/llama-swap/internal/store"
	"github.com/mostlygeek/llama-swap/internal/store/memory"
	"github.com/mostlygeek/llama-swap/internal/store/sqlite"
)

// newRepo builds a CaptureRepository for one backend, sized so the budget is
// reachable: each backend enforces maxBytes its own way, and every assertion
// here is about behaviour they must share.
type newRepo func(t *testing.T, maxBytes, maxItemBytes int64) store.CaptureRepository

var captureBackends = map[string]newRepo{
	"memory": func(t *testing.T, maxBytes, maxItemBytes int64) store.CaptureRepository {
		return memory.New(maxBytes, maxItemBytes)
	},
	"sqlite": func(t *testing.T, maxBytes, maxItemBytes int64) store.CaptureRepository {
		st, err := sqlite.New(sqlite.Options{
			Path:                 filepath.Join(t.TempDir(), "activity.db"),
			CapturesPath:         filepath.Join(t.TempDir(), "captures.db"),
			CapturesMaxBytes:     maxBytes,
			CapturesMaxItemBytes: maxItemBytes,
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			if err := st.Close(); err != nil {
				t.Errorf("store.Close: %v", err)
			}
		})

		return st.Captures()
	},
}

// The proxy talks to a store.CaptureRepository and never learns which backend
// it got, so the contract is asserted once for all of them: a new backend is
// accepted by these subtests, not by a copy of the server's tests.
func TestCaptures_BackendsSatisfyTheRepositoryContract(t *testing.T) {
	const (
		budget   = 100_000
		itemMax  = 40_000
		blobSize = 30_000
	)

	for name, build := range captureBackends {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()

			t.Run("PutGetRoundtrip", func(t *testing.T) {
				repo := build(t, budget, itemMax)
				data := blobOf(blobSize)
				require.NoError(t, repo.Put(ctx, 7, data))

				got, found, err := repo.Get(ctx, 7)
				require.NoError(t, err)
				require.True(t, found)
				assert.Equal(t, data, got)

				got, found, err = repo.Get(ctx, 999)
				require.NoError(t, err)
				require.False(t, found, "an id with no capture must report not found")
				assert.Nil(t, got)
			})

			t.Run("PutReplaces", func(t *testing.T) {
				repo := build(t, budget, itemMax)
				require.NoError(t, repo.Put(ctx, 1, []byte("first")))
				require.NoError(t, repo.Put(ctx, 1, []byte("second")))

				got, found, err := repo.Get(ctx, 1)
				require.NoError(t, err)
				require.True(t, found)
				assert.Equal(t, []byte("second"), got)
			})

			t.Run("HasIsBatched", func(t *testing.T) {
				repo := build(t, budget, itemMax)
				for _, id := range []int{2, 4, 6} {
					require.NoError(t, repo.Put(ctx, id, []byte("x")))
				}

				found, err := repo.Has(ctx, []int{2, 3, 4, 5, 6})
				require.NoError(t, err)
				assert.Equal(t, map[int]bool{2: true, 4: true, 6: true}, found)

				found, err = repo.Has(ctx, nil)
				require.NoError(t, err)
				assert.Empty(t, found)
			})

			t.Run("Delete", func(t *testing.T) {
				repo := build(t, budget, itemMax)
				require.NoError(t, repo.Put(ctx, 1, []byte("aaaaaaaaaa")))
				require.NoError(t, repo.Delete(ctx, 1))

				_, found, err := repo.Get(ctx, 1)
				require.NoError(t, err)
				require.False(t, found)

				// Deleting an id that holds nothing is not an error.
				require.NoError(t, repo.Delete(ctx, 1))
			})

			// The proxy logs and skips this error, so it must never arrive
			// with captures already evicted to make room for a blob that was
			// never going to fit.
			t.Run("OversizedCaptureIsRejectedWithoutEviction", func(t *testing.T) {
				repo := build(t, budget, itemMax)
				require.NoError(t, repo.Put(ctx, 1, blobOf(blobSize)))

				err := repo.Put(ctx, 2, blobOf(itemMax+1))
				require.Error(t, err)
				require.True(t, errors.Is(err, store.ErrCaptureTooLarge), "got %v", err)

				_, found, err := repo.Get(ctx, 1)
				require.NoError(t, err)
				require.True(t, found, "a rejected capture must not evict stored ones")
			})

			// Writing past the budget drops the oldest captures and keeps the
			// one just written: the newest request is the one the operator
			// wants to read.
			t.Run("BudgetEvictsOldestAndKeepsNewest", func(t *testing.T) {
				repo := build(t, budget, itemMax)
				for id := 1; id <= 6; id++ {
					require.NoError(t, repo.Put(ctx, id, blobOf(blobSize)))
				}

				found, err := repo.Has(ctx, []int{1, 6})
				require.NoError(t, err)
				assert.Contains(t, found, 6, "the newest capture must survive the budget")
				assert.NotContains(t, found, 1, "eviction must start at the oldest capture")
			})

			// Captures are written from a detached context after the handler
			// returns, so a repository that ignores cancellation would keep
			// writing into a store the caller has abandoned.
			t.Run("CanceledContextFailsEveryVerb", func(t *testing.T) {
				repo := build(t, budget, itemMax)
				canceled, cancel := context.WithCancel(ctx)
				cancel()

				assert.Error(t, repo.Put(canceled, 1, []byte("x")))
				_, _, err := repo.Get(canceled, 1)
				assert.Error(t, err)
				_, err = repo.Has(canceled, []int{1})
				assert.Error(t, err)
				assert.Error(t, repo.Delete(canceled, 1))
			})
		})
	}
}

func blobOf(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}
