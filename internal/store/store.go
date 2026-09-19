// Package store defines the data access contracts for llama-swap's persisted
// data. It holds the domain types and repository interfaces only; concrete
// backends live in subpackages such as store/sqlite. Callers depend on the
// interfaces here so a backend can be swapped or faked without touching them.
package store

import "context"

// Store is the root of data access. It groups one repository per kind of
// persisted data, all backed by the same underlying database, and owns that
// database's lifecycle.
type Store interface {
	// Activity returns the repository for request activity log rows.
	Activity() ActivityRepository

	// IsInMemory reports whether the backing database is not persisted to
	// disk. In-memory stores are bounded by pruning rather than by disk.
	IsInMemory() bool

	// Close releases the backing database. It is safe to call more than once.
	Close() error
}

// ActivityRepository persists and queries activity log entries.
type ActivityRepository interface {
	// Insert stores an entry and returns it with its assigned ID. A zero
	// Timestamp is replaced with the current time.
	Insert(ctx context.Context, entry ActivityLogEntry) (ActivityLogEntry, error)

	// List returns one page of entries matching the query, newest first
	// unless the query says otherwise. Page and Limit are normalized with
	// ActivityQuery.WithDefaults before use.
	List(ctx context.Context, query ActivityQuery) (ActivityPage, error)

	// Stats aggregates totals and speed histograms over the matching entries.
	Stats(ctx context.Context, query ActivityStatsQuery) (ActivityStats, error)

	// Prune deletes all but the newest maxRows entries. A maxRows of zero or
	// less is a no-op.
	Prune(ctx context.Context, maxRows int) error
}
