-- +goose Up
CREATE TABLE activity (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts_created INTEGER NOT NULL,
    model_id TEXT NOT NULL,
    req_path TEXT NOT NULL DEFAULT '',
    resp_content_type TEXT NOT NULL DEFAULT '',
    resp_status_code INTEGER NOT NULL DEFAULT 0,
    cache_tokens INTEGER NOT NULL DEFAULT 0,
    draft_tokens INTEGER NOT NULL DEFAULT 0,
    draft_acc_tokens INTEGER NOT NULL DEFAULT 0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    prompt_per_second REAL NOT NULL DEFAULT 0,
    tokens_per_second REAL NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    error_msg TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_activity_created_id
    ON activity (ts_created DESC, id DESC);

CREATE INDEX idx_activity_model_created_id
    ON activity (model_id, ts_created DESC, id DESC);

-- +goose Down
DROP TABLE activity;
