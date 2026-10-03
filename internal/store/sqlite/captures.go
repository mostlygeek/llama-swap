package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/store"
)

// captureRepository implements store.CaptureRepository over the captures table
// of its own database file.
//
// The size budget is measured on disk — the main file plus its WAL — rather
// than as a stored total of blob bytes. SQLite reuses freed pages, so a
// database that has once reached its budget keeps its high-water mark until
// the pages are handed back; enforceBudget releases them on the spot, which is
// what lets the next check see the drop.
type captureRepository struct {
	db *sql.DB
	// path is the database file, so the budget can be measured against the
	// file the operating system actually sees.
	path string
	// maxBytes is the total budget for the database on disk. Eviction frees
	// the oldest captures until the file fits. Zero means no limit.
	maxBytes int64
	// maxItemBytes is the largest single blob that can be stored. Zero means
	// no per-capture limit.
	maxItemBytes int64
}

var _ store.CaptureRepository = (*captureRepository)(nil)

func (r *captureRepository) Put(ctx context.Context, id int, data []byte) error {
	size := int64(len(data))
	if r.maxItemBytes > 0 && size > r.maxItemBytes {
		return fmt.Errorf("%w: capture %d is %d bytes, limit is %d bytes", store.ErrCaptureTooLarge, id, size, r.maxItemBytes)
	}
	if r.maxBytes > 0 && size > r.maxBytes {
		return fmt.Errorf("%w: capture %d is %d bytes, the whole budget is %d bytes", store.ErrCaptureTooLarge, id, size, r.maxBytes)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("put capture: %w", err)
	}
	defer tx.Rollback()

	// A capture is keyed by activity row ID, so a write to an ID that already
	// has one replaces it.
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

	// Budget enforcement runs after the commit: it reads the file, and pages
	// only reach the file once the transaction is in it.
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

// hasBatchIDs bounds the placeholders per Has query. A statement's variable
// count is capped by SQLite (32766 in modernc.org/sqlite) and the activity
// page size is caller-supplied, so a large page is split into batches rather
// than failing the whole lookup.
const hasBatchIDs = 500

func (r *captureRepository) Has(ctx context.Context, ids []int) (map[int]bool, error) {
	found := make(map[int]bool, len(ids))

	for start := 0; start < len(ids); start += hasBatchIDs {
		batch := ids[start:min(start+hasBatchIDs, len(ids))]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch))
		for _, id := range batch {
			args = append(args, id)
		}

		rows, err := r.db.QueryContext(ctx,
			fmt.Sprintf(`SELECT id FROM captures WHERE id IN (%s)`, placeholders), args...,
		)
		if err != nil {
			return nil, fmt.Errorf("has capture: %w", err)
		}

		var scanErr error
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				scanErr = err
				break
			}
			found[id] = true
		}
		if scanErr == nil {
			scanErr = rows.Err()
		}
		rows.Close()
		if scanErr != nil {
			return nil, fmt.Errorf("has capture: %w", scanErr)
		}
	}

	return found, nil
}

func (r *captureRepository) Delete(ctx context.Context, id int) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM captures WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete capture: %w", err)
	}
	// Hand the pages back and fold the WAL in, so a delete shows up in the
	// budget immediately.
	if err := r.releasePages(ctx); err != nil {
		return err
	}
	return r.checkpoint(ctx)
}

// enforceBudget evicts the oldest captures until the database fits its budget,
// then releases the freed pages.
//
// It deletes the minimum needed: the excess over the budget, and no more. That
// is only stable because the pages are released and the WAL is folded back into
// the file in the same breath. Without that, the file sits at its high-water
// mark, every later write reads itself as over budget, and eviction never
// stops.
//
// It also runs once at open, which is what enforces a budget lowered in config
// while the process was not running.
func (r *captureRepository) enforceBudget(ctx context.Context) error {
	if r.maxBytes <= 0 {
		return nil
	}

	usage, err := r.diskUsage()
	if err != nil {
		return fmt.Errorf("captures budget: %w", err)
	}
	if usage <= r.maxBytes {
		return nil
	}

	// The eviction decision has to be made against the file, not the WAL:
	// pages sit in the WAL until a checkpoint folds them in, and pages freed
	// by a delete leave the file unchanged. Checkpointing first makes the
	// measurement, and every measurement after it, mean the same thing.
	if err := r.checkpoint(ctx); err != nil {
		return err
	}
	usage, err = r.diskUsage()
	if err != nil {
		return fmt.Errorf("captures budget: %w", err)
	}
	excess := usage - r.maxBytes
	if excess <= 0 {
		return nil
	}

	freed, err := r.evictOldest(ctx, excess)
	if err != nil {
		return fmt.Errorf("captures budget: %w", err)
	}
	if freed == 0 {
		return nil
	}
	if err := r.releasePages(ctx); err != nil {
		return err
	}
	return r.checkpoint(ctx)
}

// configureBudget bounds the WAL, which the budget is measured against. Left
// at its defaults the WAL grows to 4 MiB before SQLite checkpoints it, which
// exceeds a small budget on its own and makes every write look over budget.
// Both settings are derived from the budget, so the WAL stays a fixed small
// share of it at any size.
func (r *captureRepository) configureBudget(ctx context.Context) error {
	if r.maxBytes <= 0 {
		return nil
	}

	// A sixteenth of the budget, and never under 64 KiB: small budgets still
	// get a WAL big enough that SQLite is not checkpointing constantly.
	walLimit := max(r.maxBytes/16, 64<<10)
	pragmas := []string{
		// Truncate the WAL to this size when it is checkpointed.
		fmt.Sprintf(`PRAGMA journal_size_limit = %d`, walLimit),
		// Checkpoint about once per walLimit of writes.
		fmt.Sprintf(`PRAGMA wal_autocheckpoint = %d`, max(walLimit/4096, 16)),
	}
	for _, pragma := range pragmas {
		if _, err := r.db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("configure captures budget: %w", err)
		}
	}
	return nil
}

// checkpoint folds the WAL into the database file and truncates it, so the
// file size reflects everything committed. A capture write at the budget costs
// about 50 microseconds with it, 10 microseconds without, which is why it runs
// on the eviction path and not on every write.
func (r *captureRepository) checkpoint(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("captures budget: %w", err)
	}
	return nil
}

// evictOldest deletes the oldest captures, by id, until at least excess bytes
// are freed, and returns the bytes freed. Each step is a primary-key seek, so
// the work is proportional to the rows evicted — usually one. An excess larger
// than the whole store empties the table and stops.
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

// releasePages returns free pages to the operating system, shrinking the file.
// The cost is proportional to the pages released (measured at about 6
// microseconds per 4 KiB page), which is why it runs right after an eviction
// instead of on a schedule.
//
// This needs the database opened with PRAGMA auto_vacuum = INCREMENTAL, set
// before the file is created: see openDatabase.
func (r *captureRepository) releasePages(ctx context.Context) error {
	var freelist int64
	if err := r.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&freelist); err != nil {
		return fmt.Errorf("captures budget: %w", err)
	}
	if freelist <= 0 {
		return nil
	}

	// PRAGMA arguments are part of the statement text and take no bound
	// parameter. freelist is our own integer, not caller data.
	if _, err := r.db.ExecContext(ctx,
		fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, freelist),
	); err != nil {
		return fmt.Errorf("captures budget: %w", err)
	}
	return nil
}

// diskUsage is what the captures database occupies on disk: the main file plus
// the WAL that has not been checkpointed into it yet. configureBudget bounds
// that WAL at a fixed share of the budget, so the budget is enforced to within
// a few percent of its megabyte setting rather than to the byte.
func (r *captureRepository) diskUsage() (int64, error) {
	var total int64
	for _, path := range []string{r.path, r.path + "-wal"} {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}
		total += info.Size()
	}
	return total, nil
}
