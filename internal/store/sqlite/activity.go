package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/store"
)

// activityRepository implements store.ActivityRepository over the activity
// table.
type activityRepository struct {
	db *sql.DB
}

var _ store.ActivityRepository = (*activityRepository)(nil)

// activitySortColumns maps the store's sortable keys onto SQL columns. Only
// these column names ever reach a query; TestActivity_SortColumnsCoverStoreKeys
// checks the map stays in step with store.ActivitySortKeys.
var activitySortColumns = map[string]string{
	"id":                "id",
	"time":              "ts_created",
	"model":             "model_id",
	"src":               "src",
	"req_path":          "req_path",
	"resp_status_code":  "resp_status_code",
	"resp_content_type": "resp_content_type",
	"cached":            "cache_tokens",
	"prompt":            "input_tokens",
	"generated":         "output_tokens",
	"drafted":           "draft_tokens",
	"prompt_speed":      "prompt_per_second",
	"gen_speed":         "tokens_per_second",
	"duration":          "duration_ms",
}

func (r *activityRepository) Insert(ctx context.Context, entry store.ActivityLogEntry) (store.ActivityLogEntry, error) {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	metadataJSON, err := marshalMetadata(entry.Metadata)
	if err != nil {
		return store.ActivityLogEntry{}, err
	}

	res, err := r.db.ExecContext(ctx, `
		INSERT INTO activity (
			ts_created, src, model_id, req_path, resp_content_type, resp_status_code,
			cache_tokens, draft_tokens, draft_acc_tokens, input_tokens, output_tokens,
			prompt_per_second, tokens_per_second, duration_ms, error_msg, metadata_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.Timestamp.Unix(),
		entry.Src,
		entry.Model,
		entry.ReqPath,
		entry.RespContentType,
		entry.RespStatusCode,
		entry.Tokens.CachedTokens,
		entry.Tokens.DraftTokens,
		entry.Tokens.DraftAccTokens,
		entry.Tokens.InputTokens,
		entry.Tokens.OutputTokens,
		entry.Tokens.PromptPerSecond,
		entry.Tokens.TokensPerSecond,
		entry.DurationMs,
		entry.ErrorMsg,
		metadataJSON,
	)
	if err != nil {
		return store.ActivityLogEntry{}, fmt.Errorf("insert activity: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return store.ActivityLogEntry{}, fmt.Errorf("insert activity id: %w", err)
	}
	entry.ID = int(id)
	return entry, nil
}

func (r *activityRepository) List(ctx context.Context, query store.ActivityQuery) (store.ActivityPage, error) {
	query = query.WithDefaults()
	offset := (query.Page - 1) * query.Limit

	where, args := activityWhere(query.ActivityFilter)
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM activity`+where, args...).Scan(&total); err != nil {
		return store.ActivityPage{}, fmt.Errorf("count activity: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT
			id, ts_created, src, model_id, req_path, resp_content_type, resp_status_code,
			cache_tokens, draft_tokens, draft_acc_tokens, input_tokens, output_tokens,
			prompt_per_second, tokens_per_second, duration_ms, error_msg, metadata_json
		FROM activity`+where+activityOrderBy(query)+`
		LIMIT ? OFFSET ?`,
		append(args, query.Limit, offset)...,
	)
	if err != nil {
		return store.ActivityPage{}, fmt.Errorf("list activity: %w", err)
	}
	defer rows.Close()

	entries := []store.ActivityLogEntry{}
	for rows.Next() {
		entry, err := scanActivity(rows)
		if err != nil {
			return store.ActivityPage{}, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return store.ActivityPage{}, fmt.Errorf("list activity rows: %w", err)
	}

	return store.ActivityPage{
		Data:       entries,
		Page:       query.Page,
		Limit:      query.Limit,
		Total:      total,
		TotalPages: store.TotalPages(total, query.Limit),
	}, nil
}

func (r *activityRepository) Stats(ctx context.Context, query store.ActivityStatsQuery) (store.ActivityStats, error) {
	filter := store.ActivityFilter{}
	if model := strings.TrimSpace(query.Model); model != "" {
		filter.Models = []string{model}
	}
	where, args := activityWhere(filter)
	row := r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(CASE WHEN cache_tokens > 0 THEN cache_tokens ELSE 0 END), 0)
		FROM activity`+where, args...)

	var stats store.ActivityStats
	if err := row.Scan(&stats.TotalRequests, &stats.TotalInputTokens, &stats.TotalOutputTokens, &stats.TotalCacheTokens); err != nil {
		return store.ActivityStats{}, fmt.Errorf("activity stats: %w", err)
	}

	promptValues, genValues, err := r.speedValues(ctx, where, args)
	if err != nil {
		return store.ActivityStats{}, err
	}
	stats.PromptHistogram = store.NewHistogram(promptValues)
	stats.GenerationHistogram = store.NewHistogram(genValues)
	return stats, nil
}

// speedValues reads both histogram source columns in a single scan. Zero
// values mean the speed was not reported and are excluded per column. No
// ORDER BY: store.NewHistogram sorts the values itself.
func (r *activityRepository) speedValues(ctx context.Context, where string, args []any) (prompt, gen []float64, err error) {
	filter := where
	if filter == "" {
		filter = ` WHERE prompt_per_second > 0 OR tokens_per_second > 0`
	} else {
		filter += ` AND (prompt_per_second > 0 OR tokens_per_second > 0)`
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT prompt_per_second, tokens_per_second FROM activity`+filter, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("activity histogram: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var promptValue, genValue float64
		if err := rows.Scan(&promptValue, &genValue); err != nil {
			return nil, nil, fmt.Errorf("activity histogram row: %w", err)
		}
		if promptValue > 0 {
			prompt = append(prompt, promptValue)
		}
		if genValue > 0 {
			gen = append(gen, genValue)
		}
	}
	return prompt, gen, rows.Err()
}

func (r *activityRepository) Prune(ctx context.Context, maxRows int) error {
	if maxRows <= 0 {
		return nil
	}
	// AUTOINCREMENT ids are monotonic and never reused, so the rows beyond
	// the newest maxRows are exactly those with id <= MAX(id) - maxRows.
	// One statement keeps the per-insert prune cheap; if ids ever become
	// sparse this retains fewer than maxRows rows, which is fine for a
	// bounded recent-activity cap.
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM activity WHERE id <= (SELECT MAX(id) FROM activity) - ?`, maxRows,
	); err != nil {
		return fmt.Errorf("prune activity: %w", err)
	}
	return nil
}

// activityOrderBy builds a safe ORDER BY clause from the query's sort key and
// direction. Unknown sort keys fall back to "id". A secondary "id" sort keeps
// pagination stable when the primary column has duplicate values.
func activityOrderBy(query store.ActivityQuery) string {
	column, ok := activitySortColumns[query.Sort]
	if !ok {
		column = "id"
	}
	direction := "DESC"
	if strings.EqualFold(query.Order, "asc") {
		direction = "ASC"
	}
	if column == "id" {
		return " ORDER BY id " + direction
	}
	return " ORDER BY " + column + " " + direction + ", id " + direction
}

// activityWhere builds a parameterized WHERE clause from a filter. It returns
// an empty string when nothing is filtered. Callers append further conditions
// with " AND ...", so the clause is always a single unparenthesized conjunction.
func activityWhere(filter store.ActivityFilter) (string, []any) {
	var conditions []string
	var args []any

	models := make([]string, 0, len(filter.Models))
	for _, model := range filter.Models {
		if model = strings.TrimSpace(model); model != "" {
			models = append(models, model)
		}
	}
	if len(models) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(models)), ", ")
		conditions = append(conditions, "model_id IN ("+placeholders+")")
		for _, model := range models {
			args = append(args, model)
		}
	}

	if !filter.Start.IsZero() {
		conditions = append(conditions, "ts_created >= ?")
		args = append(args, filter.Start.Unix())
	}
	if !filter.End.IsZero() {
		conditions = append(conditions, "ts_created <= ?")
		args = append(args, filter.End.Unix())
	}
	if filter.MinID > 0 {
		conditions = append(conditions, "id >= ?")
		args = append(args, filter.MinID)
	}
	if filter.MaxID > 0 {
		conditions = append(conditions, "id <= ?")
		args = append(args, filter.MaxID)
	}
	if filter.SrcPrefix != "" {
		// A bounded lexical range is a literal prefix match (SQL wildcard
		// characters remain data) and can use idx_activity_src_created_id.
		conditions = append(conditions, "src >= ? AND src < ?")
		args = append(args, filter.SrcPrefix, filter.SrcPrefix+"\U0010FFFF")
	}

	if len(conditions) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

func marshalMetadata(metadata map[string]string) (string, error) {
	if len(metadata) == 0 {
		return "", nil
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("marshal activity metadata: %w", err)
	}
	return string(data), nil
}

type activityScanner interface {
	Scan(dest ...any) error
}

func scanActivity(scanner activityScanner) (store.ActivityLogEntry, error) {
	var entry store.ActivityLogEntry
	var ts int64
	var metadataJSON string
	if err := scanner.Scan(
		&entry.ID,
		&ts,
		&entry.Src,
		&entry.Model,
		&entry.ReqPath,
		&entry.RespContentType,
		&entry.RespStatusCode,
		&entry.Tokens.CachedTokens,
		&entry.Tokens.DraftTokens,
		&entry.Tokens.DraftAccTokens,
		&entry.Tokens.InputTokens,
		&entry.Tokens.OutputTokens,
		&entry.Tokens.PromptPerSecond,
		&entry.Tokens.TokensPerSecond,
		&entry.DurationMs,
		&entry.ErrorMsg,
		&metadataJSON,
	); err != nil {
		return store.ActivityLogEntry{}, fmt.Errorf("scan activity: %w", err)
	}
	entry.Timestamp = time.Unix(ts, 0)
	if metadataJSON != "" {
		if err := json.Unmarshal([]byte(metadataJSON), &entry.Metadata); err != nil {
			return store.ActivityLogEntry{}, fmt.Errorf("unmarshal activity metadata: %w", err)
		}
	}
	return entry, nil
}
