-- +goose Up
CREATE TABLE cache (
    key         TEXT PRIMARY KEY,
    data        BLOB NOT NULL,
    ttl_seconds INTEGER NOT NULL DEFAULT 0,
    ts_created  INTEGER NOT NULL
);

-- Expiry scans compare ts_created + ttl_seconds against now, so only rows
-- that actually carry a TTL are worth indexing.
CREATE INDEX idx_cache_expiry
    ON cache (ts_created)
    WHERE ttl_seconds > 0;

-- +goose Down
DROP INDEX idx_cache_expiry;
DROP TABLE cache;
