-- +goose Up
-- data holds the encoded capture blob; size is denormalized for the budget's
-- SUM. Capture ids follow the activity table's, so ORDER BY id is
-- oldest-first.
CREATE TABLE captures (
    id   INTEGER PRIMARY KEY,
    data BLOB NOT NULL,
    size INTEGER NOT NULL
);

-- +goose Down
DROP TABLE captures;
