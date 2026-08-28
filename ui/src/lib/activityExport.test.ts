import { describe, it, expect } from "vitest";
import type { ActivityLogEntry } from "./types";
import { formatAbsoluteTime } from "./format";
import {
  activityCellText,
  buildActivityMarkdown,
  formatDrafted,
  summarizeActivity,
  summaryMarkdown,
  toMarkdownTable,
} from "./activityExport";

function entry(overrides: Partial<ActivityLogEntry> = {}): ActivityLogEntry {
  return {
    id: 1,
    timestamp: new Date(2026, 5, 25, 12, 34, 56).toISOString(),
    model: "qwen3",
    req_path: "/v1/chat/completions",
    resp_content_type: "application/json",
    resp_status_code: 200,
    duration_ms: 1000,
    has_capture: false,
    ...overrides,
    tokens: {
      cache_tokens: 0,
      draft_tokens: 0,
      draft_acc_tokens: 0,
      input_tokens: 0,
      output_tokens: 0,
      prompt_per_second: -1,
      tokens_per_second: -1,
      ...(overrides.tokens ?? {}),
    },
  };
}

describe("formatDrafted", () => {
  it("reports the acceptance rate", () => {
    expect(formatDrafted(10, 7)).toBe("70.0% (7/10)");
  });

  it("renders a dash when nothing was drafted", () => {
    expect(formatDrafted(0, 0)).toBe("-");
  });
});

describe("summarizeActivity", () => {
  it("returns zeroed totals and unknown speeds for no rows", () => {
    expect(summarizeActivity([])).toEqual({
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
    });
  });

  it("tracks the oldest and newest row regardless of their order", () => {
    const oldest = new Date(2026, 7, 16, 9, 0, 0).toISOString();
    const newest = new Date(2026, 7, 16, 9, 45, 0).toISOString();
    const summary = summarizeActivity([
      entry({ timestamp: new Date(2026, 7, 16, 9, 30, 0).toISOString() }),
      entry({ timestamp: newest }),
      entry({ timestamp: oldest }),
    ]);

    expect(summary.startedAt).toBe(oldest);
    expect(summary.endedAt).toBe(newest);
  });

  it("sums token counts and durations", () => {
    const summary = summarizeActivity([
      entry({
        duration_ms: 1500,
        tokens: {
          cache_tokens: 100,
          draft_tokens: 10,
          draft_acc_tokens: 6,
          input_tokens: 200,
          output_tokens: 50,
          prompt_per_second: 100,
          tokens_per_second: 25,
        },
      }),
      entry({
        duration_ms: 2500,
        tokens: {
          cache_tokens: 20,
          draft_tokens: 30,
          draft_acc_tokens: 12,
          input_tokens: 300,
          output_tokens: 150,
          prompt_per_second: 300,
          tokens_per_second: 75,
        },
      }),
    ]);

    expect(summary.durationMs).toBe(4000);
    expect(summary.cacheTokens).toBe(120);
    expect(summary.inputTokens).toBe(500);
    expect(summary.outputTokens).toBe(200);
    expect(summary.draftTokens).toBe(40);
    expect(summary.draftAccTokens).toBe(18);
  });

  it("weights average speeds by tokens rather than by request", () => {
    // 200 tokens in 2s + 300 tokens in 1s => 500 tokens in 3s.
    const summary = summarizeActivity([
      entry({
        tokens: {
          cache_tokens: 0,
          draft_tokens: 0,
          draft_acc_tokens: 0,
          input_tokens: 200,
          output_tokens: 0,
          prompt_per_second: 100,
          tokens_per_second: -1,
        },
      }),
      entry({
        tokens: {
          cache_tokens: 0,
          draft_tokens: 0,
          draft_acc_tokens: 0,
          input_tokens: 300,
          output_tokens: 0,
          prompt_per_second: 300,
          tokens_per_second: -1,
        },
      }),
    ]);

    expect(summary.promptPerSecond).toBeCloseTo(500 / 3, 6);
    // A plain per-request mean would have been 200.
    expect(summary.promptPerSecond).not.toBeCloseTo(200, 6);
    expect(summary.tokensPerSecond).toBe(-1);
  });

  it("ignores rows with unreported speeds or no tokens", () => {
    const summary = summarizeActivity([
      entry({
        tokens: {
          cache_tokens: 0,
          draft_tokens: 0,
          draft_acc_tokens: 0,
          input_tokens: 100,
          output_tokens: 40,
          prompt_per_second: -1,
          tokens_per_second: 20,
        },
      }),
      entry({
        tokens: {
          cache_tokens: 0,
          draft_tokens: 0,
          draft_acc_tokens: 0,
          input_tokens: 0,
          output_tokens: 0,
          prompt_per_second: 999,
          tokens_per_second: 999,
        },
      }),
    ]);

    expect(summary.promptPerSecond).toBe(-1);
    expect(summary.tokensPerSecond).toBeCloseTo(20, 6);
  });
});

describe("activityCellText", () => {
  const row = entry({
    id: 42,
    model: "llama-3",
    req_path: "/v1/embeddings",
    resp_status_code: 500,
    resp_content_type: "text/event-stream",
    duration_ms: 2500,
    metadata: { user: "bob", tier: "gold" },
    tokens: {
      cache_tokens: 1024,
      draft_tokens: 10,
      draft_acc_tokens: 5,
      input_tokens: 2048,
      output_tokens: 512,
      prompt_per_second: 123.456,
      tokens_per_second: -1,
    },
  });

  it("renders each column as the table shows it", () => {
    expect(activityCellText(row, "id")).toBe("42");
    expect(activityCellText(row, "time")).toBe("2026-06-25 12:34:56");
    expect(activityCellText(row, "model")).toBe("llama-3");
    expect(activityCellText(row, "req_path")).toBe("/v1/embeddings");
    expect(activityCellText(row, "resp_status_code")).toBe("500");
    expect(activityCellText(row, "resp_content_type")).toBe("text/event-stream");
    expect(activityCellText(row, "cached")).toBe((1024).toLocaleString());
    expect(activityCellText(row, "prompt")).toBe((2048).toLocaleString());
    expect(activityCellText(row, "generated")).toBe((512).toLocaleString());
    expect(activityCellText(row, "drafted")).toBe("50.0% (5/10)");
    expect(activityCellText(row, "prompt_speed")).toBe("123.46 t/s");
    expect(activityCellText(row, "gen_speed")).toBe("unknown");
    expect(activityCellText(row, "duration")).toBe("2.50s");
    expect(activityCellText(row, "meta")).toBe("user=bob; tier=gold");
  });

  it("renders dashes for empty values", () => {
    const empty = entry({ req_path: "", resp_content_type: "", resp_status_code: 0 });
    expect(activityCellText(empty, "req_path")).toBe("-");
    expect(activityCellText(empty, "resp_content_type")).toBe("-");
    expect(activityCellText(empty, "resp_status_code")).toBe("-");
    expect(activityCellText(empty, "cached")).toBe("-");
    expect(activityCellText(empty, "drafted")).toBe("-");
    expect(activityCellText(empty, "meta")).toBe("-");
  });

  it("returns an empty string for columns without a text form", () => {
    expect(activityCellText(row, "capture")).toBe("");
    expect(activityCellText(row, "nope")).toBe("");
  });
});

describe("toMarkdownTable", () => {
  it("renders columns in the given order", () => {
    const markdown = toMarkdownTable(
      [entry({ id: 7, duration_ms: 1000 })],
      [
        { id: "duration", label: "Duration" },
        { id: "id", label: "ID" },
      ]
    );

    expect(markdown).toBe(
      ["| Duration | ID |", "| --- | --- |", "| 1.00s | 7 |"].join("\n")
    );
  });

  it("escapes pipes and flattens line breaks", () => {
    const markdown = toMarkdownTable(
      [entry({ id: 1, metadata: { note: "a|b\nc" } })],
      [{ id: "meta", label: "Meta" }]
    );

    expect(markdown.split("\n")[2]).toBe("| note=a\\|b c |");
  });

  it("still emits a header when there are no rows", () => {
    expect(toMarkdownTable([], [{ id: "id", label: "ID" }])).toBe("| ID |\n| --- |");
  });

  it("returns an empty string when there are no columns", () => {
    expect(toMarkdownTable([entry()], [])).toBe("");
  });
});

describe("summaryMarkdown", () => {
  it("renders the totals as a two-column table", () => {
    const summary = summarizeActivity([
      entry({
        timestamp: new Date(2026, 7, 16, 9, 12, 3).toISOString(),
        duration_ms: 41900,
        tokens: {
          cache_tokens: 12480,
          draft_tokens: 240,
          draft_acc_tokens: 148,
          input_tokens: 5678,
          output_tokens: 1904,
          prompt_per_second: 412.3,
          tokens_per_second: 48.15,
        },
      }),
    ]);

    expect(summaryMarkdown(summary)).toBe(
      [
        "| Summary | |",
        "| --- | --- |",
        "| Range | 2026-08-16 09:12:03 → 2026-08-16 09:12:03 |",
        "| Duration | 41.90s |",
        `| Cached | ${(12480).toLocaleString()} |`,
        `| Prompt | ${(5678).toLocaleString()} (412.30 t/s) |`,
        `| Generated | ${(1904).toLocaleString()} (48.15 t/s) |`,
        "| Drafted | 61.7% 148/240 |",
      ].join("\n")
    );
  });

  it("reports zeros and unknown speeds for an empty page", () => {
    expect(summaryMarkdown(summarizeActivity([]))).toBe(
      [
        "| Summary | |",
        "| --- | --- |",
        "| Range | - |",
        "| Duration | 0.00s |",
        "| Cached | 0 |",
        "| Prompt | 0 (unknown) |",
        "| Generated | 0 (unknown) |",
        "| Drafted | - |",
      ].join("\n")
    );
  });
});

describe("buildActivityMarkdown", () => {
  it("puts the summary above the data table", () => {
    const markdown = buildActivityMarkdown([entry({ id: 7, duration_ms: 1000 })], [
      { id: "id", label: "ID" },
    ]);

    expect(markdown.startsWith("| Summary | |\n")).toBe(true);
    expect(markdown).toContain("\n\n| ID |\n| --- |\n| 7 |");
  });

  it("closes with an attribution line carrying the export timestamp", () => {
    const markdown = buildActivityMarkdown(
      [entry({ id: 7 })],
      [{ id: "id", label: "ID" }],
      new Date(2026, 7, 16, 9, 30, 0)
    );

    expect(markdown.endsWith(
      "\n\nExported from [llama-swap](https://github.com/mostlygeek/llama-swap) at 2026-08-16 09:30:00"
    )).toBe(true);
  });

  it("defaults the export timestamp to now", () => {
    expect(buildActivityMarkdown([entry()], [{ id: "id", label: "ID" }])).toContain(
      `at ${formatAbsoluteTime(new Date().toISOString())}`
    );
  });

  it("emits the summary and attribution when every column is hidden", () => {
    const generatedAt = new Date(2026, 7, 16, 9, 30, 0);
    expect(buildActivityMarkdown([entry()], [], generatedAt)).toBe(
      summaryMarkdown(summarizeActivity([entry()])) +
        "\n\nExported from [llama-swap](https://github.com/mostlygeek/llama-swap) at 2026-08-16 09:30:00"
    );
  });
});
