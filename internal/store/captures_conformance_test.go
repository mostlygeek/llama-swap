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

// newRepo builds a CaptureRepository for one backend.
type newRepo func(t *testing.T, maxBytes int64) store.CaptureRepository

var captureBackends = map[string]newRepo{
	"memory": func(t *testing.T, maxBytes int64) store.CaptureRepository {
		return memory.New(maxBytes)
	},
	"sqlite": func(t *testing.T, maxBytes int64) store.CaptureRepository {
		st, err := sqlite.New(sqlite.Options{
			Path:             filepath.Join(t.TempDir(), "activity.db"),
			CapturesPath:     filepath.Join(t.TempDir(), "captures.db"),
			CapturesMaxBytes: maxBytes,
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

func TestCaptures_BackendsSatisfyTheRepositoryContract(t *testing.T) {
	const (
		budget   = 100_000
		blobSize = 30_000
	)

	for name, build := range captureBackends {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()

			t.Run("PutGetRoundtrip", func(t *testing.T) {
				repo := build(t, budget)
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
				repo := build(t, budget)
				require.NoError(t, repo.Put(ctx, 1, []byte("first")))
				require.NoError(t, repo.Put(ctx, 1, []byte("second")))

				got, found, err := repo.Get(ctx, 1)
				require.NoError(t, err)
				require.True(t, found)
				assert.Equal(t, []byte("second"), got)
			})

			t.Run("HasReportsStoredIDs", func(t *testing.T) {
				repo := build(t, budget)
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

			t.Run("OversizedCaptureIsRejectedWithoutEviction", func(t *testing.T) {
				repo := build(t, budget)
				require.NoError(t, repo.Put(ctx, 1, blobOf(blobSize)))

				err := repo.Put(ctx, 2, blobOf(budget+1))
				require.Error(t, err)
				require.True(t, errors.Is(err, store.ErrCaptureTooLarge), "got %v", err)

				_, found, err := repo.Get(ctx, 1)
				require.NoError(t, err)
				require.True(t, found, "a rejected capture must not evict stored ones")
			})

			t.Run("BudgetEvictsOldestAndKeepsNewest", func(t *testing.T) {
				repo := build(t, budget)
				for id := 1; id <= 6; id++ {
					require.NoError(t, repo.Put(ctx, id, blobOf(blobSize)))
				}

				found, err := repo.Has(ctx, []int{1, 6})
				require.NoError(t, err)
				assert.Contains(t, found, 6, "the newest capture must survive the budget")
				assert.NotContains(t, found, 1, "eviction must start at the oldest capture")
			})

			t.Run("CanceledContextFailsEveryVerb", func(t *testing.T) {
				repo := build(t, budget)
				canceled, cancel := context.WithCancel(ctx)
				cancel()

				assert.Error(t, repo.Put(canceled, 1, []byte("x")))
				_, _, err := repo.Get(canceled, 1)
				assert.Error(t, err)
				_, err = repo.Has(canceled, []int{1})
				assert.Error(t, err)
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
