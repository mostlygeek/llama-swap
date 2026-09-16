-- +goose Up
ALTER TABLE activity ADD COLUMN src TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_activity_src_created_id
    ON activity (src, ts_created DESC, id DESC);

-- +goose Down
DROP INDEX idx_activity_src_created_id;
ALTER TABLE activity DROP COLUMN src;
