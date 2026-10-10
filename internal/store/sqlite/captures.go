package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/store"
)

// captureRepository implements store.CaptureRepository over the captures
// table of its own database file. The budget counts stored blob bytes, not
// file bytes: SQLite reuses the pages an eviction frees, so the file keeps
// its high-water mark.
type captureRepository struct {
	db *sql.DB
	// databaseID is the id of the activity database this repository's ids come
	// from. Every row it writes carries it and every read is limited to it, so
	// ids from two activity databases can share one captures file without ever
	// naming the same request.
	databaseID string
	// maxBytes is the total budget for stored blob bytes; zero means no
	// limit.
	maxBytes int64
}

var _ store.CaptureRepository = (*captureRepository)(nil)

func (r *captureRepository) Put(ctx context.Context, id int, data []byte) error {
	size := int64(len(data))
	if r.maxBytes > 0 && size > r.maxBytes {
		return fmt.Errorf("%w: capture %d is %d bytes, the whole budget is %d bytes", store.ErrCaptureTooLarge, id, size, r.maxBytes)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("put capture: %w", err)
	}
	defer tx.Rollback()

	// A repeated id replaces rather than conflicts.
	if _, err := tx.ExecContext(ctx, `DELETE FROM captures WHERE db = ? AND id = ?`, r.databaseID, id); err != nil {
		return fmt.Errorf("put capture: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO captures (db, id, created, data, size) VALUES (?, ?, ?, ?, ?)`,
		r.databaseID, id, time.Now().Unix(), data, size,
	); err != nil {
		return fmt.Errorf("put capture: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("put capture: %w", err)
	}

	return r.enforceBudget(ctx)
}

func (r *captureRepository) Get(ctx context.Context, id int) ([]byte, bool, error) {
	var data []byte
	err := r.db.QueryRowContext(ctx, `SELECT data FROM captures WHERE db = ? AND id = ?`, r.databaseID, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get capture: %w", err)
	}
	return data, true, nil
}

// Has reports which of ids hold a capture.
func (r *captureRepository) Has(ctx context.Context, ids []int) (map[int]bool, error) {
	found := make(map[int]bool, len(ids))
	if len(ids) == 0 {
		return found, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}

	rows, err := r.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT id FROM captures WHERE db = ? AND id IN (%s)`, placeholders),
		append([]any{r.databaseID}, args...)...,
	)
	if err != nil {
		return nil, fmt.Errorf("has capture: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("has capture: %w", err)
		}
		found[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("has capture: %w", err)
	}

	return found, nil
}

// enforceBudget evicts the oldest captures until the stored bytes fit the
// budget. It also runs at open, to enforce a budget lowered while the
// process was not running.
func (r *captureRepository) enforceBudget(ctx context.Context) error {
	if r.maxBytes <= 0 {
		return nil
	}

	stored, err := r.storedBytes(ctx)
	if err != nil {
		return fmt.Errorf("captures budget: %w", err)
	}

	excess := stored - r.maxBytes
	if excess <= 0 {
		return nil
	}
	if _, err := r.evictOldest(ctx, excess); err != nil {
		return fmt.Errorf("captures budget: %w", err)
	}
	return nil
}

// storedBytes totals the size column: the quantity the budget counts.
func (r *captureRepository) storedBytes(ctx context.Context) (int64, error) {
	var stored int64
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(size), 0) FROM captures`,
	).Scan(&stored)
	return stored, err
}

// evictOldest deletes the oldest captures until at least excess bytes are
// freed. Write time orders the rows, not id: a captures file holds rows from
// every activity database that has used it, and ids restart at 1 in each of
// them. Rows written in the same second are freed in insert order.
func (r *captureRepository) evictOldest(ctx context.Context, excess int64) (int64, error) {
	if excess <= 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var freed int64
	for freed < excess {
		var rowid int64
		var size int64
		// rowid is the insert order within the file, which breaks the ties a
		// one-second timestamp leaves.
		err := tx.QueryRowContext(ctx,
			`SELECT rowid, size FROM captures ORDER BY created ASC, rowid ASC LIMIT 1`,
		).Scan(&rowid, &size)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM captures WHERE rowid = ?`, rowid); err != nil {
			return 0, err
		}
		freed += size
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return freed, nil
}
