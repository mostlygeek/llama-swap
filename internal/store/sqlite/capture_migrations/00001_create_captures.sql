-- +goose Up
-- One row per captured request. id is the capture_id recorded on the activity
-- row; a capture does not otherwise depend on the activity database, so either
-- side can be missing. data is the zstd-compressed CBOR capture and size is
-- its length, kept as a column so pruning by total size does not read the
-- blobs. Pruning deletes in rowid (insertion) order.
CREATE TABLE captures (
    id         TEXT PRIMARY KEY,
    ts_created INTEGER NOT NULL,
    size       INTEGER NOT NULL,
    data       BLOB NOT NULL
);

-- +goose Down
DROP TABLE captures;
