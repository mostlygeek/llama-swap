package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Key identifies a keyvalue entry. Keys are constants declared in this package
// and registered in kvKeys. Because a key is a constant, the type of its value
// is fixed by the code that reads and writes it, so the type stays implicit in
// the GetKV/SetKV type parameter rather than being recorded in the database.
//
// Reading a key with a different type than it was written with fails with an
// unmarshal error.
type Key string

// kvKeys is the set of valid keys. The bool reports whether the value may be
// read through the HTTP API; false keeps the value private to the Go code.
//
// Add a key by declaring the constant and registering it here:
//
//	const KeyUISettings Key = "ui.settings"
//
//	var kvKeys = map[Key]bool{
//		KeyUISettings: true,
//	}
var kvKeys = map[Key]bool{}

// PublicKVKey resolves a raw string to a registered key that may be read
// through the HTTP API. Private and unknown keys both report false so the API
// cannot be used to probe for internal keys.
func PublicKVKey(raw string) (Key, bool) {
	public, ok := kvKeys[Key(raw)]
	if !ok || !public {
		return "", false
	}
	return Key(raw), true
}

// RegisterKVKeyForTest registers key until the returned function is called. It
// lets packages outside store exercise GetKV, SetKV and the HTTP API without
// depending on which keys the application happens to declare. Not safe for use
// from parallel tests.
func RegisterKVKeyForTest(key Key, public bool) func() {
	kvKeys[key] = public
	return func() {
		delete(kvKeys, key)
	}
}

// SetKV stores value as JSON under key, replacing any value already stored.
func SetKV[T any](ctx context.Context, s *Store, key Key, value T) error {
	if _, ok := kvKeys[key]; !ok {
		return fmt.Errorf("unknown keyvalue key: %q", key)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal keyvalue %q: %w", key, err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO keyvalue (key, value_json, ts_updated) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value_json = excluded.value_json,
			ts_updated = excluded.ts_updated`,
		string(key),
		string(data),
		time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("set keyvalue %q: %w", key, err)
	}
	return nil
}

// GetKV loads the value stored under key. The bool reports whether the key has
// a stored value; a key that was never set returns the zero value of T, false
// and a nil error.
func GetKV[T any](ctx context.Context, s *Store, key Key) (T, bool, error) {
	var value T
	if _, ok := kvKeys[key]; !ok {
		return value, false, fmt.Errorf("unknown keyvalue key: %q", key)
	}

	var valueJSON string
	err := s.db.QueryRowContext(ctx, `SELECT value_json FROM keyvalue WHERE key = ?`, string(key)).Scan(&valueJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return value, false, nil
	}
	if err != nil {
		return value, false, fmt.Errorf("get keyvalue %q: %w", key, err)
	}
	if err := json.Unmarshal([]byte(valueJSON), &value); err != nil {
		var zero T
		return zero, false, fmt.Errorf("unmarshal keyvalue %q: %w", key, err)
	}
	return value, true, nil
}
