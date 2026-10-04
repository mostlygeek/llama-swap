-- +goose Up
-- meta holds store-level key/value facts. database_id must equal the activity
-- database's value; a mismatch means the two files do not belong together.
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- +goose Down
DROP TABLE meta;
