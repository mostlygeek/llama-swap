package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
)

// databaseIDKey names the meta row that identifies an activity database. The
// captures table tags every row with it, so one captures file can hold the
// captures of several activity databases without their ids ever naming the
// same request.
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

// newDatabaseID returns a random identifier for an activity database.
func newDatabaseID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate database id: %w", err)
	}
	return fmt.Sprintf("%x", b), nil
}
