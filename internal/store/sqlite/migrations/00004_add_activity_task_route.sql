-- +goose Up
ALTER TABLE activity ADD COLUMN task TEXT NOT NULL DEFAULT '';
ALTER TABLE activity ADD COLUMN route TEXT NOT NULL DEFAULT '';
ALTER TABLE activity ADD COLUMN peer_id TEXT NOT NULL DEFAULT '';
ALTER TABLE activity ADD COLUMN reasoning_tokens INTEGER NOT NULL DEFAULT -1;

-- Backfill task for rows recorded before the column existed. The mapping
-- matches meteredRoutes in internal/server. /upstream/... rows stay empty.
UPDATE activity SET task = CASE req_path
    WHEN '/v1/chat/completions' THEN 'chat'
    WHEN '/v1/responses' THEN 'chat'
    WHEN '/v1/completions' THEN 'chat'
    WHEN '/v1/messages' THEN 'chat'
    WHEN '/completion' THEN 'chat'
    WHEN '/infill' THEN 'chat'
    WHEN '/v/chat/completions' THEN 'chat'
    WHEN '/v/responses' THEN 'chat'
    WHEN '/v/completions' THEN 'chat'
    WHEN '/v/messages' THEN 'chat'
    WHEN '/v1/embeddings' THEN 'embedding'
    WHEN '/v/embeddings' THEN 'embedding'
    WHEN '/rerank' THEN 'rerank'
    WHEN '/reranking' THEN 'rerank'
    WHEN '/v1/rerank' THEN 'rerank'
    WHEN '/v1/reranking' THEN 'rerank'
    WHEN '/v/rerank' THEN 'rerank'
    WHEN '/v/reranking' THEN 'rerank'
    WHEN '/v1/messages/count_tokens' THEN 'token_count'
    WHEN '/v/messages/count_tokens' THEN 'token_count'
    WHEN '/v1/images/generations' THEN 'image_generation'
    WHEN '/sdapi/v1/txt2img' THEN 'image_generation'
    WHEN '/v1/images/edits' THEN 'image_edit'
    WHEN '/sdapi/v1/img2img' THEN 'image_edit'
    WHEN '/v1/audio/speech' THEN 'speech'
    WHEN '/v1/audio/transcriptions' THEN 'transcription'
    WHEN '/v1/systemone' THEN 'decision'
    WHEN '/audioapi/v1/tasks/run' THEN 'audio'
    ELSE ''
END;

-- +goose Down
ALTER TABLE activity DROP COLUMN reasoning_tokens;
ALTER TABLE activity DROP COLUMN peer_id;
ALTER TABLE activity DROP COLUMN route;
ALTER TABLE activity DROP COLUMN task;
