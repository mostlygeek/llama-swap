-- +goose Up
CREATE TABLE keyvalue (
    key TEXT PRIMARY KEY,
    value_json TEXT NOT NULL DEFAULT '',
    ts_updated INTEGER NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE keyvalue;
