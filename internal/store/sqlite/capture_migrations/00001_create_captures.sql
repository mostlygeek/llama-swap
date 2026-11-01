-- +goose Up
-- One row per captured request. id is the capture_id recorded on the activity
-- row, yymmddhh-xxxx (UTC hour and four random base58 characters); a capture
-- does not otherwise depend on the activity database, so either side can be
-- missing. A duplicate id is rejected rather than overwritten.
--
-- data is the zstd-compressed CBOR capture. compressed_bytes is the length of
-- data and is what captureBuffer limits; uncompressed_bytes is the CBOR length
-- before compression. Both are columns so size totals and pruning never read
-- the blobs. Pruning deletes in rowid (insertion) order.
CREATE TABLE captures (
    id                 TEXT PRIMARY KEY,
    ts_created         INTEGER NOT NULL,
    compressed_bytes   INTEGER NOT NULL,
    uncompressed_bytes INTEGER NOT NULL,
    data               BLOB NOT NULL
);

-- +goose Down
DROP TABLE captures;
