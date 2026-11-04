-- +goose Up
-- capture_id names the row's request/response capture in the captures
-- database. It is empty when no capture was taken. The capture can be missing
-- even when it is set: captures are pruned by size and live in a separate
-- file.
ALTER TABLE activity ADD COLUMN capture_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE activity DROP COLUMN capture_id;
