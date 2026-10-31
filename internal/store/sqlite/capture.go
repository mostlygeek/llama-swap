package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mostlygeek/llama-swap/internal/store"
)

// captureRepository implements store.CaptureRepository over the captures
// table of its own database.
//
// It keeps the total stored size in memory, read once when the database is
// opened, so Prune only touches the table when a write pushed the total over
// the limit. mu serializes every write with its update to total.
type captureRepository struct {
	db *sql.DB

	mu    sync.Mutex
	total int64
}

var _ store.CaptureRepository = (*captureRepository)(nil)

// hasBatchSize bounds how many ids one Has query binds, keeping each query
// well under SQLite's bound-parameter limit.
const hasBatchSize = 500

func newCaptureRepository(ctx context.Context, db *sql.DB) (*captureRepository, error) {
	r := &captureRepository{db: db}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size), 0) FROM captures`).Scan(&r.total); err != nil {
		return nil, fmt.Errorf("read captures size: %w", err)
	}
	return r, nil
}

func (r *captureRepository) Put(ctx context.Context, entry store.CaptureEntry) error {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	// ON CONFLICT DO NOTHING turns a duplicate id into zero affected rows
	// rather than a driver-specific constraint error.
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO captures (id, ts_created, size, data)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		entry.ID, entry.Timestamp.Unix(), len(entry.Data), entry.Data,
	)
	if err != nil {
		return fmt.Errorf("put capture: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("put capture: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("put capture %s: %w", entry.ID, store.ErrCaptureExists)
	}
	r.total += int64(len(entry.Data))
	return nil
}

func (r *captureRepository) Get(ctx context.Context, id string) (store.CaptureEntry, bool, error) {
	var (
		ts   int64
		data []byte
	)
	err := r.db.QueryRowContext(ctx, `SELECT ts_created, data FROM captures WHERE id = ?`, id).Scan(&ts, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return store.CaptureEntry{}, false, nil
	}
	if err != nil {
		return store.CaptureEntry{}, false, fmt.Errorf("get capture: %w", err)
	}
	return store.CaptureEntry{ID: id, Timestamp: time.Unix(ts, 0), Data: data}, true, nil
}

func (r *captureRepository) Has(ctx context.Context, ids []string) (map[string]bool, error) {
	found := make(map[string]bool)
	for start := 0; start < len(ids); start += hasBatchSize {
		batch := ids[start:min(start+hasBatchSize, len(ids))]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		rows, err := r.db.QueryContext(ctx, `SELECT id FROM captures WHERE id IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("has captures: %w", err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, fmt.Errorf("has captures: %w", err)
			}
			found[id] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("has captures: %w", err)
		}
	}
	return found, nil
}

func (r *captureRepository) Delete(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var size int64
	err := r.db.QueryRowContext(ctx, `DELETE FROM captures WHERE id = ? RETURNING size`, id).Scan(&size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete capture: %w", err)
	}
	r.total -= size
	return nil
}

func (r *captureRepository) Prune(ctx context.Context, maxBytes int64) error {
	if maxBytes <= 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	excess := r.total - maxBytes
	if excess <= 0 {
		return nil
	}

	// Walk from the oldest written capture, summing sizes until enough
	// would be freed. This reads only the rows that are about to be deleted.
	// rowid is insertion order.
	rows, err := r.db.QueryContext(ctx, `SELECT rowid, size FROM captures ORDER BY rowid ASC`)
	if err != nil {
		return fmt.Errorf("prune captures: %w", err)
	}
	var (
		cutoff int64
		freed  int64
	)
	for freed < excess && rows.Next() {
		var size int64
		if err := rows.Scan(&cutoff, &size); err != nil {
			rows.Close()
			return fmt.Errorf("prune captures: %w", err)
		}
		freed += size
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("prune captures: %w", err)
	}
	if freed == 0 {
		return nil
	}

	if _, err := r.db.ExecContext(ctx, `DELETE FROM captures WHERE rowid <= ?`, cutoff); err != nil {
		return fmt.Errorf("prune captures: %w", err)
	}
	r.total -= freed
	return nil
}

func (r *captureRepository) Size() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total
}
