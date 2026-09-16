// Package sqlite implements store.Store on top of an embedded SQLite database.
// The schema is managed by goose migrations embedded in migrations/ and
// applied when a store is opened.
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

// Store is a store.Store backed by a single SQLite database.
type Store struct {
	db       *sql.DB
	inMemory bool
	activity *activityRepository
}

var _ store.Store = (*Store)(nil)

// New opens a SQLite store at path and applies pending migrations. An empty
// path creates an in-memory store that is lost on Close.
func New(path string) (*Store, error) {
	dsn := strings.TrimSpace(path)
	diskFile := dsn != ""
	if dsn == "" {
		dsn = ":memory:"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite store: %w", err)
	}
	// A single connection keeps an in-memory database alive (each new
	// connection to ":memory:" would be a fresh empty database) and
	// serializes writers for on-disk files.
	db.SetMaxOpenConns(1)

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure sqlite store: %w", err)
	}
	if diskFile {
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
	if err := runMigrations(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{
		db:       db,
		inMemory: !diskFile,
		activity: &activityRepository{db: db},
	}, nil
}

func runMigrations(ctx context.Context, db *sql.DB) error {
	migrations, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("sqlite store migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations)
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

// IsInMemory implements store.Store.
func (s *Store) IsInMemory() bool {
	return s.inMemory
}

// Close implements store.Store.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
