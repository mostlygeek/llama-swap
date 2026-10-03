// Package sqlite implements store.Store on top of an embedded SQLite database.
// The schema is managed by goose migrations embedded in migrations/ and
// applied when a store is opened. Request/response captures are the one
// repository kept in a database of their own: see Options.CapturesPath.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/mostlygeek/llama-swap/internal/store"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

//go:embed capture_migrations/*.sql
var captureMigrationFS embed.FS

// Options configures the databases a Store opens.
type Options struct {
	// Path is the activity and cache database file. An empty path creates an
	// in-memory database that is lost on Close.
	Path string

	// CapturesPath is the database file holding request/response captures. An
	// empty path disables persistent captures: Store.Captures returns nil.
	// Captures are kept in their own file so their size budget, eviction and
	// backups stay independent of the activity log, and so a capture database
	// can be deleted without touching activity history. It must not be the
	// same file as Path.
	CapturesPath string

	// CapturesMaxBytes is the budget for the captures database on disk. A Put
	// that would take the file past it evicts the oldest captures until there
	// is room. Zero means no limit.
	CapturesMaxBytes int64

	// CapturesMaxItemBytes is the largest single capture blob Put accepts.
	// Larger captures are rejected with store.ErrCaptureTooLarge. Zero means
	// no per-capture limit.
	CapturesMaxItemBytes int64
}

// Store is a store.Store backed by embedded SQLite databases.
type Store struct {
	db       *sql.DB
	inMemory bool
	activity *activityRepository
	cache    *cacheRepository

	capturesDB *sql.DB
	captures   *captureRepository
}

var _ store.Store = (*Store)(nil)

// New opens the databases described by opts and applies pending migrations to
// each of them.
func New(opts Options) (*Store, error) {
	dsn := strings.TrimSpace(opts.Path)
	inMemory := dsn == ""
	if inMemory {
		dsn = ":memory:"
	}

	ctx := context.Background()
	db, err := openDatabase(ctx, dsn, migrationFS, "migrations", false)
	if err != nil {
		return nil, err
	}

	s := &Store{
		db:       db,
		inMemory: inMemory,
		activity: &activityRepository{db: db},
		cache:    &cacheRepository{db: db},
	}
	// Drop cache rows that expired while the process was not running, so a
	// long-lived database does not accumulate them.
	if err := s.cache.Prune(ctx); err != nil {
		db.Close()
		return nil, err
	}

	if capturesPath := strings.TrimSpace(opts.CapturesPath); capturesPath != "" {
		if err := s.openCaptures(ctx, capturesPath, dsn, opts); err != nil {
			db.Close()
			return nil, err
		}
	}

	return s, nil
}

// openCaptures opens the capture database and enforces its size budget.
// capturesPath must differ from the activity database's DSN: the two are
// separate files by design.
func (s *Store) openCaptures(ctx context.Context, capturesPath, activityDSN string, opts Options) error {
	if capturesPath == activityDSN {
		return fmt.Errorf("open captures: %s is also the activity database; captures need their own file", capturesPath)
	}

	capturesDB, err := openDatabase(ctx, capturesPath, captureMigrationFS, "capture_migrations", true)
	if err != nil {
		return err
	}

	repo := &captureRepository{
		db:           capturesDB,
		path:         capturesPath,
		maxBytes:     opts.CapturesMaxBytes,
		maxItemBytes: opts.CapturesMaxItemBytes,
	}
	// Bound the WAL the budget is measured against, then enforce the budget
	// before serving: maxSizeMB may have been lowered in config while the
	// process was not running.
	if err := repo.configureBudget(ctx); err != nil {
		capturesDB.Close()
		return err
	}
	if err := repo.enforceBudget(ctx); err != nil {
		capturesDB.Close()
		return err
	}

	s.capturesDB = capturesDB
	s.captures = repo
	return nil
}

// openDatabase opens one SQLite file with the pragmas every llama-swap
// database uses, then applies the migrations embedded under dir.
//
// autoVacuum must be requested before the file header is written: SQLite
// reports PRAGMA auto_vacuum as 0 and ignores incremental_vacuum when the
// pragma is issued after journal_mode has created the file. Setting it first
// costs nothing on databases that do not need it.
func openDatabase(ctx context.Context, dsn string, migrations embed.FS, dir string, autoVacuum bool) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite store: %w", err)
	}
	// A single connection keeps an in-memory database alive (each new
	// connection to ":memory:" would be a fresh empty database) and
	// serializes writers for on-disk files.
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure sqlite store: %w", err)
	}
	if autoVacuum {
		// Lets the capture repository hand pages back after evicting; see
		// captureRepository.releasePages.
		if _, err := db.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure sqlite store: %w", err)
		}
	}
	if dsn != ":memory:" {
		var mode string
		if err := db.QueryRowContext(ctx, `PRAGMA journal_mode = WAL`).Scan(&mode); err != nil {
			db.Close()
			return nil, fmt.Errorf("enable sqlite store WAL mode: %w", err)
		}
		if !strings.EqualFold(mode, "wal") {
			db.Close()
			return nil, fmt.Errorf("enable sqlite store WAL mode: got %q", mode)
		}
	}
	if err := runMigrations(ctx, db, migrations, dir); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func runMigrations(ctx context.Context, db *sql.DB, migrations embed.FS, dir string) error {
	sub, err := fs.Sub(migrations, dir)
	if err != nil {
		return fmt.Errorf("sqlite store migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		return fmt.Errorf("sqlite store migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("sqlite store migrations up: %w", err)
	}
	return nil
}

// Activity implements store.Store.
func (s *Store) Activity() store.ActivityRepository {
	return s.activity
}

// Cache implements store.Store.
func (s *Store) Cache() store.CacheRepository {
	return s.cache
}

// Captures implements store.Store. It returns nil when persistent captures
// are disabled.
func (s *Store) Captures() store.CaptureRepository {
	if s.captures == nil {
		return nil
	}
	return s.captures
}

// IsInMemory implements store.Store. It reports the activity database; the
// capture database is always a file on disk.
func (s *Store) IsInMemory() bool {
	return s.inMemory
}

// Close implements store.Store.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var capturesErr error
	if s.capturesDB != nil {
		capturesErr = s.capturesDB.Close()
	}
	if s.db == nil {
		return capturesErr
	}
	if err := s.db.Close(); err != nil {
		return err
	}
	return capturesErr
}
