package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
)

// databaseIDKey names the meta row that identifies which activity database a
// file belongs to. The activity and captures databases write the same value;
// a mismatch means one of the two files was replaced.
const databaseIDKey = "database_id"

// ensureDatabaseID returns db's database id, creating one the first time the
// database is opened.
func ensureDatabaseID(ctx context.Context, db *sql.DB) (string, error) {
	id, found, err := readDatabaseID(ctx, db)
	if err != nil || found {
		return id, err
	}

	generated, err := newDatabaseID()
	if err != nil {
		return "", err
	}
	// A concurrent opener may win the insert; read back whichever value stuck.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`,
		databaseIDKey, generated,
	); err != nil {
		return "", fmt.Errorf("create database id: %w", err)
	}
	id, _, err = readDatabaseID(ctx, db)
	return id, err
}

// readDatabaseID returns the stored database id and whether one exists.
func readDatabaseID(ctx context.Context, db *sql.DB) (string, bool, error) {
	var id string
	err := db.QueryRowContext(ctx,
		`SELECT value FROM meta WHERE key = ?`, databaseIDKey).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read database id: %w", err)
	}
	return id, true, nil
}

// writeDatabaseID records id, replacing any existing value.
func writeDatabaseID(ctx context.Context, db *sql.DB, id string) error {
	if _, err := db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		databaseIDKey, id,
	); err != nil {
		return fmt.Errorf("write database id: %w", err)
	}
	return nil
}

// newDatabaseID returns a random identifier for a store's database pair.
func newDatabaseID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate database id: %w", err)
	}
	return fmt.Sprintf("%x", b), nil
}

// rotateDatabaseFile moves path, and any SQLite sidecar files, to the lowest
// unused "<path>.N" and returns the new path.
func rotateDatabaseFile(path string) (string, error) {
	var target string
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s.%d", path, i)
		switch _, err := os.Stat(candidate); {
		case errors.Is(err, os.ErrNotExist):
			target = candidate
		case err != nil:
			return "", fmt.Errorf("rotate database %s: %w", path, err)
		}
		if target != "" {
			break
		}
	}

	if err := os.Rename(path, target); err != nil {
		return "", fmt.Errorf("rotate database %s: %w", path, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); err != nil {
			continue
		}
		if err := os.Rename(path+suffix, target+suffix); err != nil {
			return "", fmt.Errorf("rotate database %s%s: %w", path, suffix, err)
		}
	}
	return target, nil
}
