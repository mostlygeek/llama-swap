-- +goose Up
-- One row per captured request. id is the activity row's id in the activity
-- database and ts_created is that row's ts_created; together they let the
-- store detect a captures file that does not belong to its activity file.
-- data is the zstd-compressed CBOR capture and size is its length, kept as a
-- column so pruning by total size does not read the blobs.
CREATE TABLE captures (
    id         INTEGER PRIMARY KEY,
    ts_created INTEGER NOT NULL,
    size       INTEGER NOT NULL,
    data       BLOB NOT NULL
);

-- +goose Down
DROP TABLE captures;
