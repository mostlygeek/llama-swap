-- +goose Up
CREATE TABLE activity_totals (
    model_id TEXT PRIMARY KEY,
    request_count INTEGER NOT NULL DEFAULT 0
);

-- Seed totals for stores upgraded from a schema that did not have the
-- lifetime aggregate. Future inserts keep this table ahead of activity
-- pruning, while existing rows provide the best recoverable baseline.
INSERT INTO activity_totals (model_id, request_count)
SELECT
    model_id,
    COUNT(*)
FROM activity
GROUP BY model_id;

-- +goose Down
DROP TABLE activity_totals;
