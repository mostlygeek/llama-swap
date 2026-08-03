-- +goose Up
CREATE TABLE keyvalue (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE keyvalue;
