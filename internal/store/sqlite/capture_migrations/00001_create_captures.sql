-- +goose Up
-- Captures live in their own database file, keyed by the activity row ID they
-- belong to. data holds the opaque encoded blob (CBOR compressed with zstd);
-- size is denormalized so eviction can total freed bytes without reading a
-- single blob.
--
-- IDs arrive in increasing order (they come from the activity table's
-- AUTOINCREMENT and writes are serialized), so ORDER BY id is oldest-first
-- and the primary key covers eviction without a second index.
CREATE TABLE captures (
    id   INTEGER PRIMARY KEY,
    data BLOB NOT NULL,
    size INTEGER NOT NULL
);

-- +goose Down
DROP TABLE captures;
