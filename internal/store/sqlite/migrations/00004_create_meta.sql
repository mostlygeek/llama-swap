-- +goose Up
-- meta holds store-level key/value facts. database_id identifies which
-- captures database belongs to this activity database.
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- +goose Down
DROP TABLE meta;
