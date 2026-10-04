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
	"os"
	"path/filepath"
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
	// empty path disables persistent captures: Store.Captures returns nil. It
	// must not be the same file as Path.
	CapturesPath string

	// CapturesMaxBytes is the total budget for the stored capture bytes;
	// zero means no limit.
	CapturesMaxBytes int64
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
	db, err := openDatabase(ctx, dsn, migrationFS, "migrations")
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
func (s *Store) openCaptures(ctx context.Context, capturesPath, activityDSN string, opts Options) error {
	if capturesPath == activityDSN {
		return fmt.Errorf("open captures: %s is also the activity database; captures need their own file", capturesPath)
	}

	capturesDB, err := openDatabase(ctx, capturesPath, captureMigrationFS, "capture_migrations")
	if err != nil {
		return err
	}

	repo := &captureRepository{
		db:       capturesDB,
		maxBytes: opts.CapturesMaxBytes,
	}
	// Enforce a budget lowered while the process was not running.
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
func openDatabase(ctx context.Context, dsn string, migrations embed.FS, dir string) (*sql.DB, error) {
	if dsn != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(dsn), 0755); err != nil {
			return nil, fmt.Errorf("create sqlite store directory: %w", err)
		}
	}
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

// IsInMemory implements store.Store.
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
