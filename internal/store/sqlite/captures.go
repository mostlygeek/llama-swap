package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/store"
)

// captureRepository implements store.CaptureRepository over the captures
// table of its own database file. The budget counts stored blob bytes, not
// file bytes: SQLite reuses the pages an eviction frees, so the file keeps
// its high-water mark.
type captureRepository struct {
	db *sql.DB
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM captures WHERE id = ?`, id); err != nil {
		return fmt.Errorf("put capture: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO captures (id, data, size) VALUES (?, ?, ?)`, id, data, size,
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
	err := r.db.QueryRowContext(ctx, `SELECT data FROM captures WHERE id = ?`, id).Scan(&data)
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
		fmt.Sprintf(`SELECT id FROM captures WHERE id IN (%s)`, placeholders), args...,
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

// evictOldest deletes the oldest captures, by id, until at least excess
// bytes are freed.
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
		var id int
		var size int64
		err := tx.QueryRowContext(ctx,
			`SELECT id, size FROM captures ORDER BY id LIMIT 1`,
		).Scan(&id, &size)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM captures WHERE id = ?`, id); err != nil {
			return 0, err
		}
		freed += size
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return freed, nil
}
