package sqlite

import (
	"testing"

	"github.com/mostlygeek/llama-swap/internal/store"
)

// Every sort key the store package accepts must map to a SQL column here, and
// nothing outside that whitelist may be sortable.
func TestActivity_SortColumnsCoverStoreKeys(t *testing.T) {
	keys := store.ActivitySortKeys()
	for _, key := range keys {
		if _, ok := activitySortColumns[key]; !ok {
			t.Errorf("store sort key %q has no SQL column", key)
		}
	}
	if len(activitySortColumns) != len(keys) {
		t.Errorf("activitySortColumns has %d entries, store has %d keys", len(activitySortColumns), len(keys))
	}
	for key := range activitySortColumns {
		if !store.ValidActivitySortKey(key) {
			t.Errorf("SQL sort column key %q is not a store sort key", key)
		}
	}
}
