// Summary and markdown-export helpers for the activity table. Kept free of
// Svelte so the arithmetic and rendering rules can be unit tested directly.

import type { ActivityLogEntry } from "./types";
import { formatAbsoluteTime, formatDuration, formatSpeed } from "./format";

/** Format a draft acceptance rate as "p% (accepted/drafted)". */
export function formatDrafted(drafted: number, accepted: number): string {
  return drafted > 0
    ? ((accepted * 100) / drafted).toFixed(1) + "% (" + accepted + "/" + drafted + ")"
    : "-";
}

/** Totals and average speeds across a set of activity rows. */
export interface ActivitySummary {
  durationMs: number;
  cacheTokens: number;
  inputTokens: number;
  outputTokens: number;
  draftTokens: number;
  draftAccTokens: number;
  /** Token-weighted average, or -1 when no row reported a usable speed. */
  promptPerSecond: number;
  /** Token-weighted average, or -1 when no row reported a usable speed. */
  tokensPerSecond: number;
  /** Timestamp of the oldest row, or "" when there are none. */
  startedAt: string;
  /** Timestamp of the newest row, or "" when there are none. */
  endedAt: string;
}

/**
 * Sum a page of activity rows.
 *
 * Speeds are token-weighted (total tokens / total time) rather than a plain
 * mean of the per-request rates, so a handful of tiny requests cannot skew the
 * figure away from actual throughput. Rows without tokens, or whose speed the
 * upstream did not report (a negative value, see formatSpeed), contribute to
 * neither side of the ratio.
 */
export function summarizeActivity(rows: ActivityLogEntry[]): ActivitySummary {
  const summary: ActivitySummary = {
    durationMs: 0,
    cacheTokens: 0,
    inputTokens: 0,
    outputTokens: 0,
    draftTokens: 0,
    draftAccTokens: 0,
    promptPerSecond: -1,
    tokensPerSecond: -1,
    startedAt: "",
    endedAt: "",
  };

  let promptTokens = 0;
  let promptSeconds = 0;
  let genTokens = 0;
  let genSeconds = 0;
  // The rows arrive in whatever order the table is sorted by, so the range is
  // tracked by parsed time rather than by taking the first and last row.
  let startedMs = Infinity;
  let endedMs = -Infinity;

  for (const row of rows) {
    const rowMs = new Date(row.timestamp).getTime();
    if (!Number.isNaN(rowMs)) {
      if (rowMs < startedMs) {
        startedMs = rowMs;
        summary.startedAt = row.timestamp;
      }
      if (rowMs > endedMs) {
        endedMs = rowMs;
        summary.endedAt = row.timestamp;
      }
    }

    summary.durationMs += row.duration_ms;
    summary.cacheTokens += row.tokens.cache_tokens;
    summary.inputTokens += row.tokens.input_tokens;
    summary.outputTokens += row.tokens.output_tokens;
    summary.draftTokens += row.tokens.draft_tokens;
    summary.draftAccTokens += row.tokens.draft_acc_tokens;

    if (row.tokens.input_tokens > 0 && row.tokens.prompt_per_second > 0) {
      promptTokens += row.tokens.input_tokens;
      promptSeconds += row.tokens.input_tokens / row.tokens.prompt_per_second;
    }
    if (row.tokens.output_tokens > 0 && row.tokens.tokens_per_second > 0) {
      genTokens += row.tokens.output_tokens;
      genSeconds += row.tokens.output_tokens / row.tokens.tokens_per_second;
    }
  }

  if (promptSeconds > 0) summary.promptPerSecond = promptTokens / promptSeconds;
  if (genSeconds > 0) summary.tokensPerSecond = genTokens / genSeconds;

  return summary;
}

/**
 * Plain-text value for a table column, mirroring what the column's cell
 * renderer shows. The time column is rendered as an absolute timestamp because
 * an exported "5m ago" is meaningless once pasted somewhere else.
 */
export function activityCellText(row: ActivityLogEntry, columnId: string): string {
  switch (columnId) {
    case "id":
      return String(row.id);
    case "time":
      return formatAbsoluteTime(row.timestamp);
    case "model":
      return row.model;
    case "req_path":
      return row.req_path || "-";
    case "resp_status_code":
      return String(row.resp_status_code || "-");
    case "resp_content_type":
      return row.resp_content_type || "-";
    case "cached":
      return row.tokens.cache_tokens > 0 ? row.tokens.cache_tokens.toLocaleString() : "-";
    case "prompt":
      return row.tokens.input_tokens.toLocaleString();
    case "generated":
      return row.tokens.output_tokens.toLocaleString();
    case "drafted":
      return formatDrafted(row.tokens.draft_tokens, row.tokens.draft_acc_tokens);
    case "prompt_speed":
      return formatSpeed(row.tokens.prompt_per_second);
    case "gen_speed":
      return formatSpeed(row.tokens.tokens_per_second);
    case "duration":
      return formatDuration(row.duration_ms);
    case "meta": {
      const entries = Object.entries(row.metadata || {});
      return entries.length > 0
        ? entries.map(([key, value]) => `${key}=${value}`).join("; ")
        : "-";
    }
    default:
      return "";
  }
}

/**
 * Render the page totals as a two-column markdown table. Speeds are annotated
 * in parentheses next to the token counts they describe.
 */
export function summaryMarkdown(summary: ActivitySummary): string {
  const drafted =
    summary.draftTokens > 0
      ? `${((summary.draftAccTokens * 100) / summary.draftTokens).toFixed(1)}% ${
          summary.draftAccTokens
        }/${summary.draftTokens}`
      : "-";

  const range =
    summary.startedAt !== "" && summary.endedAt !== ""
      ? `${formatAbsoluteTime(summary.startedAt)} → ${formatAbsoluteTime(summary.endedAt)}`
      : "-";

  const rows: [string, string][] = [
    ["Range", range],
    ["Duration", formatDuration(summary.durationMs)],
    ["Cached", summary.cacheTokens.toLocaleString()],
    [
      "Prompt",
      `${summary.inputTokens.toLocaleString()} (${formatSpeed(summary.promptPerSecond)})`,
    ],
    [
      "Generated",
      `${summary.outputTokens.toLocaleString()} (${formatSpeed(summary.tokensPerSecond)})`,
    ],
    ["Drafted", drafted],
  ];

  return [
    "| Summary | |",
    "| --- | --- |",
    ...rows.map(([label, value]) => `| ${label} | ${escapeCell(value)} |`),
  ].join("\n");
}

export interface MarkdownColumn {
  id: string;
  label: string;
}

// Cell values are free-form (metadata especially), so pipes are escaped and
// any line break is flattened to keep every row on one line.
function escapeCell(value: string): string {
  return value.replace(/\|/g, "\\|").replace(/[\r\n\t]+/g, " ");
}

/** Render rows as GitHub-flavored markdown table source, columns in order. */
export function toMarkdownTable(
  rows: ActivityLogEntry[],
  columns: MarkdownColumn[]
): string {
  if (columns.length === 0) return "";
  const lines = [
    `| ${columns.map((column) => escapeCell(column.label)).join(" | ")} |`,
    `| ${columns.map(() => "---").join(" | ")} |`,
  ];
  for (const row of rows) {
    lines.push(
      `| ${columns.map((column) => escapeCell(activityCellText(row, column.id))).join(" | ")} |`
    );
  }
  return lines.join("\n");
}

/**
 * The full export: a summary table for the rows, the rows themselves, and an
 * attribution line. generatedAt is injectable so the output is testable.
 */
export function buildActivityMarkdown(
  rows: ActivityLogEntry[],
  columns: MarkdownColumn[],
  generatedAt: Date = new Date()
): string {
  const attribution =
    "Exported from [llama-swap](https://github.com/mostlygeek/llama-swap) at " +
    formatAbsoluteTime(generatedAt.toISOString());

  return [summaryMarkdown(summarizeActivity(rows)), toMarkdownTable(rows, columns), attribution]
    .filter((section) => section !== "")
    .join("\n\n");
}
