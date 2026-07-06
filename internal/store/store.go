package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// TokenMetrics holds token usage and performance metrics.
type TokenMetrics struct {
	CachedTokens    int     `json:"cache_tokens"`
	DraftTokens     int     `json:"draft_tokens"`
	DraftAccTokens  int     `json:"draft_acc_tokens"`
	InputTokens     int     `json:"input_tokens"`
	OutputTokens    int     `json:"output_tokens"`
	PromptPerSecond float64 `json:"prompt_per_second"`
	TokensPerSecond float64 `json:"tokens_per_second"`
}

// ActivityLogEntry represents parsed token statistics from llama-server logs.
type ActivityLogEntry struct {
	ID              int               `json:"id"`
	Timestamp       time.Time         `json:"timestamp"`
	Model           string            `json:"model"`
	ReqPath         string            `json:"req_path"`
	RespContentType string            `json:"resp_content_type"`
	RespStatusCode  int               `json:"resp_status_code"`
	Tokens          TokenMetrics      `json:"tokens"`
	DurationMs      int               `json:"duration_ms"`
	HasCapture      bool              `json:"has_capture"`
	ErrorMsg        string            `json:"error_msg,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type ActivityQuery struct {
	Model string
	Limit int
	Page  int
	Sort  string // sortable column key, empty defaults to "id"
	Order string // "asc" or "desc", empty defaults to "desc"
}

// activitySortColumns whitelists the sortable API keys and maps them to their
// underlying SQL columns. Keys mirror the UI column ids. The whitelist keeps
// user-supplied sort input from reaching the query as raw SQL.
var activitySortColumns = map[string]string{
	"id":                "id",
	"time":              "ts_created",
	"model":             "model_id",
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

// ActivitySortColumn returns the SQL column for a sortable API key and whether
// the key is valid.
func ActivitySortColumn(key string) (string, bool) {
	col, ok := activitySortColumns[key]
	return col, ok
}

type ActivityPage struct {
	Data       []ActivityLogEntry `json:"data"`
	Page       int                `json:"page"`
	Limit      int                `json:"limit"`
	Total      int                `json:"total"`
	TotalPages int                `json:"total_pages"`
}

type ActivityStatsQuery struct {
	Model string
}

type ActivityStats struct {
	TotalRequests       int            `json:"total_requests"`
	TotalInputTokens    int            `json:"total_input_tokens"`
	TotalOutputTokens   int            `json:"total_output_tokens"`
	TotalCacheTokens    int            `json:"total_cache_tokens"`
	PromptHistogram     *HistogramData `json:"prompt_histogram"`
	GenerationHistogram *HistogramData `json:"gen_histogram"`
}

type HistogramData struct {
	Bins    []int   `json:"bins"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	BinSize float64 `json:"binSize"`
	P50     float64 `json:"p50"`
	P95     float64 `json:"p95"`
	P99     float64 `json:"p99"`
}

type Store struct {
	db       *sql.DB
	inMemory bool
}

// IsInMemory returns true if the store is using an in-memory database.
func (s *Store) IsInMemory() bool {
	return s.inMemory
}

// New opens a SQLite store at path. An empty path creates an in-memory store.
func New(path string) (*Store, error) {
	dsn := strings.TrimSpace(path)
	diskFile := dsn != ""
	if dsn == "" {
		dsn = ":memory:"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite store: %w", err)
	}
	db.SetMaxOpenConns(1)

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure sqlite store: %w", err)
	}
	if diskFile {
		var mode string
		if err := db.QueryRowContext(ctx, `PRAGMA journal_mode = WAL`).Scan(&mode); err != nil {
			db.Close()
			return nil, fmt.Errorf("enable sqlite store WAL mode: %w", err)
		}
		if !strings.EqualFold(mode, "wal") {
			db.Close()
			return nil, fmt.Errorf("enable sqlite store WAL mode: got %q", mode)
		}
	}
	if err := runMigrations(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, inMemory: !diskFile}, nil
}

func runMigrations(ctx context.Context, db *sql.DB) error {
	migrations, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("sqlite store migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations)
	if err != nil {
		return fmt.Errorf("sqlite store migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("sqlite store migrations up: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) InsertActivity(ctx context.Context, entry ActivityLogEntry) (ActivityLogEntry, error) {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	metadataJSON, err := marshalMetadata(entry.Metadata)
	if err != nil {
		return ActivityLogEntry{}, err
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO activity (
			ts_created, model_id, req_path, resp_content_type, resp_status_code,
			cache_tokens, draft_tokens, draft_acc_tokens, input_tokens, output_tokens,
			prompt_per_second, tokens_per_second, duration_ms, error_msg, metadata_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.Timestamp.Unix(),
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
		return ActivityLogEntry{}, fmt.Errorf("insert activity: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ActivityLogEntry{}, fmt.Errorf("insert activity id: %w", err)
	}
	entry.ID = int(id)
	return entry, nil
}

func (s *Store) ListActivity(ctx context.Context, query ActivityQuery) (ActivityPage, error) {
	query = normalizeActivityQuery(query)
	offset := (query.Page - 1) * query.Limit

	where, args := activityWhere(query.Model)
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM activity`+where, args...).Scan(&total); err != nil {
		return ActivityPage{}, fmt.Errorf("count activity: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			id, ts_created, model_id, req_path, resp_content_type, resp_status_code,
			cache_tokens, draft_tokens, draft_acc_tokens, input_tokens, output_tokens,
			prompt_per_second, tokens_per_second, duration_ms, error_msg, metadata_json
		FROM activity`+where+activityOrderBy(query)+`
		LIMIT ? OFFSET ?`,
		append(args, query.Limit, offset)...,
	)
	if err != nil {
		return ActivityPage{}, fmt.Errorf("list activity: %w", err)
	}
	defer rows.Close()

	entries := []ActivityLogEntry{}
	for rows.Next() {
		entry, err := scanActivity(rows)
		if err != nil {
			return ActivityPage{}, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return ActivityPage{}, fmt.Errorf("list activity rows: %w", err)
	}

	return ActivityPage{
		Data:       entries,
		Page:       query.Page,
		Limit:      query.Limit,
		Total:      total,
		TotalPages: calculateTotalPages(total, query.Limit),
	}, nil
}

func (s *Store) ActivityStats(ctx context.Context, query ActivityStatsQuery) (ActivityStats, error) {
	where, args := activityWhere(query.Model)
	row := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(CASE WHEN cache_tokens > 0 THEN cache_tokens ELSE 0 END), 0)
		FROM activity`+where, args...)

	var stats ActivityStats
	if err := row.Scan(&stats.TotalRequests, &stats.TotalInputTokens, &stats.TotalOutputTokens, &stats.TotalCacheTokens); err != nil {
		return ActivityStats{}, fmt.Errorf("activity stats: %w", err)
	}

	promptValues, genValues, err := s.speedValues(ctx, where, args)
	if err != nil {
		return ActivityStats{}, err
	}
	stats.PromptHistogram = calculateHistogramData(promptValues)
	stats.GenerationHistogram = calculateHistogramData(genValues)
	return stats, nil
}

// speedValues reads both histogram source columns in a single scan. Zero
// values mean the speed was not reported and are excluded per column. No
// ORDER BY: calculateHistogramData sorts the values itself.
func (s *Store) speedValues(ctx context.Context, where string, args []any) (prompt, gen []float64, err error) {
	filter := where
	if filter == "" {
		filter = ` WHERE prompt_per_second > 0 OR tokens_per_second > 0`
	} else {
		filter += ` AND (prompt_per_second > 0 OR tokens_per_second > 0)`
	}
	rows, err := s.db.QueryContext(ctx,
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

func (s *Store) PruneActivity(ctx context.Context, maxRows int) error {
	if maxRows <= 0 {
		return nil
	}
	// AUTOINCREMENT ids are monotonic and never reused, so the rows beyond
	// the newest maxRows are exactly those with id <= MAX(id) - maxRows.
	// One statement keeps the per-insert prune cheap; if ids ever become
	// sparse this retains fewer than maxRows rows, which is fine for a
	// bounded recent-activity cap.
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM activity WHERE id <= (SELECT MAX(id) FROM activity) - ?`, maxRows,
	); err != nil {
		return fmt.Errorf("prune activity: %w", err)
	}
	return nil
}

func normalizeActivityQuery(query ActivityQuery) ActivityQuery {
	if query.Page < 1 {
		query.Page = 1
	}
	if query.Limit < 1 {
		query.Limit = 25
	}
	return query
}

// activityOrderBy builds a safe ORDER BY clause from the query's sort key and
// direction. Unknown sort keys fall back to "id". A secondary "id" sort keeps
// pagination stable when the primary column has duplicate values.
func activityOrderBy(query ActivityQuery) string {
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

func calculateTotalPages(total, limit int) int {
	if total == 0 || limit <= 0 {
		return 0
	}
	return int(math.Ceil(float64(total) / float64(limit)))
}

func calculateHistogramData(values []float64) *HistogramData {
	if len(values) == 0 {
		return nil
	}

	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	minVal := sorted[0]
	maxVal := sorted[len(sorted)-1]

	p50 := percentile(sorted, 50)
	p95 := percentile(sorted, 95)
	p99 := percentile(sorted, 99)

	if minVal == maxVal {
		return &HistogramData{
			Bins:    []int{len(values)},
			Min:     minVal,
			Max:     maxVal,
			BinSize: 0,
			P50:     p50,
			P95:     p95,
			P99:     p99,
		}
	}

	const minBins = 5
	const maxBins = 20
	sturges := int(math.Ceil(math.Log2(float64(len(values))))) + 1
	binCount := min(maxBins, max(minBins, sturges))
	binSize := (maxVal - minVal) / float64(binCount)

	bins := make([]int, binCount)
	for _, value := range values {
		idx := min(int(math.Floor((value-minVal)/binSize)), binCount-1)
		bins[idx]++
	}

	return &HistogramData{
		Bins:    bins,
		Min:     minVal,
		Max:     maxVal,
		BinSize: binSize,
		P50:     p50,
		P95:     p95,
		P99:     p99,
	}
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := (p / 100) * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	fraction := rank - float64(lower)
	return sorted[lower] + fraction*(sorted[upper]-sorted[lower])
}

func activityWhere(model string) (string, []any) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", nil
	}
	return " WHERE model_id = ?", []any{model}
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

func scanActivity(scanner activityScanner) (ActivityLogEntry, error) {
	var entry ActivityLogEntry
	var ts int64
	var metadataJSON string
	if err := scanner.Scan(
		&entry.ID,
		&ts,
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
		return ActivityLogEntry{}, fmt.Errorf("scan activity: %w", err)
	}
	entry.Timestamp = time.Unix(ts, 0)
	if metadataJSON != "" {
		if err := json.Unmarshal([]byte(metadataJSON), &entry.Metadata); err != nil {
			return ActivityLogEntry{}, fmt.Errorf("unmarshal activity metadata: %w", err)
		}
	}
	return entry, nil
}
