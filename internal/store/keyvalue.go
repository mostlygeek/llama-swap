package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Keys for the key/value store. Every key must be declared here so the set of
// persisted values stays auditable and callers cannot drift apart on spelling.
const (
	// KeyInstanceID is a stable identifier generated once per store.
	KeyInstanceID = "instance_id"
)

// ErrKeyEmpty is returned when a key/value operation is given an empty key.
var ErrKeyEmpty = errors.New("keyvalue key is empty")

// GetKeyValue returns the value stored for key. An unset key returns an empty
// string and no error, so callers can treat "unset" and "empty" the same way.
func (s *Store) GetKeyValue(ctx context.Context, key string) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", ErrKeyEmpty
	}
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM keyvalue WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get keyvalue %q: %w", key, err)
	}
	return value, nil
}

// SetKeyValue stores value for key, replacing any existing value.
func (s *Store) SetKeyValue(ctx context.Context, key, value string) error {
	if strings.TrimSpace(key) == "" {
		return ErrKeyEmpty
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO keyvalue (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	); err != nil {
		return fmt.Errorf("set keyvalue %q: %w", key, err)
	}
	return nil
}
