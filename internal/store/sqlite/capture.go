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

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("put capture: %w", err)
	}
	defer tx.Rollback()

	var oldSize int64
	err = tx.QueryRowContext(ctx, `SELECT size FROM captures WHERE id = ?`, entry.ID).Scan(&oldSize)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("put capture: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO captures (id, ts_created, size, data)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			ts_created = excluded.ts_created,
			size = excluded.size,
			data = excluded.data`,
		entry.ID, entry.Timestamp.Unix(), len(entry.Data), entry.Data,
	); err != nil {
		return fmt.Errorf("put capture: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("put capture: %w", err)
	}
	r.total += int64(len(entry.Data)) - oldSize
	return nil
}

func (r *captureRepository) Get(ctx context.Context, id int) (store.CaptureEntry, bool, error) {
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

func (r *captureRepository) Has(ctx context.Context, ids []int) (map[int]bool, error) {
	found := make(map[int]bool)
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
			var id int
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

func (r *captureRepository) Delete(ctx context.Context, id int) error {
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

	// Walk from the oldest capture, summing sizes until enough would be
	// freed. This reads only the rows that are about to be deleted.
	rows, err := r.db.QueryContext(ctx, `SELECT id, size FROM captures ORDER BY id ASC`)
	if err != nil {
		return fmt.Errorf("prune captures: %w", err)
	}
	var (
		cutoff int
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

	if _, err := r.db.ExecContext(ctx, `DELETE FROM captures WHERE id <= ?`, cutoff); err != nil {
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

// checkCapturesInSync verifies that the captures database belongs to the
// activity database: the newest capture must match an activity row with the
// same id and timestamp. Captures that do not match would be shown against
// unrelated activity rows, so a mismatch is an error rather than something to
// repair silently.
func checkCapturesInSync(ctx context.Context, activityDB, capturesDB *sql.DB) error {
	var id, captureTS int64
	err := capturesDB.QueryRowContext(ctx, `SELECT id, ts_created FROM captures ORDER BY id DESC LIMIT 1`).Scan(&id, &captureTS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check captures: %w", err)
	}

	var activityTS int64
	err = activityDB.QueryRowContext(ctx, `SELECT ts_created FROM activity WHERE id = ?`, id).Scan(&activityTS)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: capture %d has no matching activity row", ErrCapturesOutOfSync, id)
	}
	if err != nil {
		return fmt.Errorf("check captures: %w", err)
	}
	if activityTS != captureTS {
		return fmt.Errorf("%w: capture %d was recorded at %d but activity row %d at %d",
			ErrCapturesOutOfSync, id, captureTS, id, activityTS)
	}
	return nil
}

// ErrCapturesOutOfSync is returned by New when the captures database does
// not belong to the activity database it is opened with.
var ErrCapturesOutOfSync = errors.New("captures database is out of sync with the activity database; use the captures file that was created with this store.path, or move it aside to start a new one")
