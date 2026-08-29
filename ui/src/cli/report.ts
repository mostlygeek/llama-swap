import type { CaseResult, SuiteSummary, GradedRun } from "./grade";

/**
 * Human and harness output.
 *
 * The markdown report is the artifact the optimizing harness reads. It carries
 * failing cases only: a full transcript dump of a 40-case suite is tens of
 * thousands of tokens of mostly-passing noise, and a harness that has to read
 * it will run out of context long before it runs out of ideas.
 */

function pct(value: number): string {
  return `${(value * 100).toFixed(1)}%`;
}

export function formatSummary(summary: SuiteSummary, label?: string): string {
  const lines = [
    "",
    `  ${label ? `${label}: ` : ""}${summary.fullyPassing}/${summary.cases} cases passed every attempt`,
    `  mean pass rate    ${pct(summary.passRate)}`,
  ];
  if (summary.attempts > summary.cases) {
    lines.push(`  flaky cases       ${summary.flaky}`);
  }
  lines.push(
    `  mean iterations   ${summary.meanIterations.toFixed(2)}`,
    `  mean duration     ${(summary.meanDurationMs / 1000).toFixed(1)}s`,
    `  tool calls        ${summary.toolCalls} (${summary.toolErrors} errored)`,
    `  no-tool answers   ${summary.noToolAnswers}/${summary.attempts}`,
  );
  if (summary.errors) {
    lines.push(`  turn errors       ${summary.errors}`);
  }
  lines.push("");
  return lines.join("\n");
}

export function formatCaseLine(result: CaseResult): string {
  const mark =
    result.passCount === result.attempts
      ? "PASS"
      : result.passCount === 0
        ? "FAIL"
        : "FLAKY";
  const count =
    result.attempts > 1 ? ` ${result.passCount}/${result.attempts}` : "";
  const failed = result.runs
    .flatMap((r) =>
      r.assertions.filter((a) => !a.ok).map((a) => `${a.kind}(${a.detail})`),
    )
    .filter((detail, i, all) => all.indexOf(detail) === i);
  const why = mark === "PASS" ? "" : `  ${failed.slice(0, 3).join(", ")}`;
  return `  ${mark.padEnd(5)}${count.padEnd(6)} ${result.caseId}${why}`;
}

function truncate(text: string, max: number): string {
  const trimmed = text.trim();
  return trimmed.length <= max
    ? trimmed
    : `${trimmed.slice(0, max)}\n[... ${trimmed.length - max} more characters]`;
}

function toolTrace(run: GradedRun): string {
  if (!run.toolCalls.length) return "_(no tools called)_";
  return run.toolCalls
    .map(
      (t, i) =>
        `${i + 1}. \`${t.name}(${truncate(t.args, 160)})\`${t.ok ? "" : " **-> tool error**"}`,
    )
    .join("\n");
}

export interface ReportContext {
  label?: string;
  model: string;
  baseUrl: string;
  systemPromptSource: string;
  groups?: string[];
  summary: SuiteSummary;
  results: CaseResult[];
}

/** A markdown digest of the failures, for the harness to read. */
export function formatFailureReport(ctx: ReportContext): string {
  const failing = ctx.results.filter((r) => r.passCount < r.attempts);

  const out: string[] = [
    `# Docs Agent eval${ctx.label ? `: ${ctx.label}` : ""}`,
    "",
    `- model: \`${ctx.model}\``,
    `- server: \`${ctx.baseUrl}\``,
    `- system prompt: ${ctx.systemPromptSource}`,
    ...(ctx.groups?.length
      ? [
          `- groups: ${[...ctx.groups]
            .sort()
            .map((group) => `\`${group}\``)
            .join(", ")}`,
        ]
      : []),
    `- **mean pass rate: ${pct(ctx.summary.passRate)}** (${ctx.summary.fullyPassing}/${ctx.summary.cases} cases clean)`,
    `- mean iterations: ${ctx.summary.meanIterations.toFixed(2)}, tool errors: ${ctx.summary.toolErrors}/${ctx.summary.toolCalls}, no-tool answers: ${ctx.summary.noToolAnswers}/${ctx.summary.attempts}`,
    "",
  ];

  if (!failing.length) {
    out.push("Every case passed every attempt.", "");
    return out.join("\n");
  }

  out.push(
    `## ${failing.length} failing case${failing.length === 1 ? "" : "s"}`,
    "",
  );

  for (const result of failing) {
    const status = result.flaky
      ? `flaky, passed ${result.passCount}/${result.attempts}`
      : "failed";
    out.push(
      `### \`${result.caseId}\` (${status})`,
      "",
      `**Q:** ${result.question}`,
      "",
    );

    // The first failing attempt is the informative one; the rest usually
    // repeat it and only cost the reader context.
    const run = result.runs.find((r) => !r.passed) ?? result.runs[0];

    out.push("**Failed assertions:**", "");
    for (const a of run.assertions.filter((x) => !x.ok)) {
      out.push(`- \`${a.kind}\`: ${a.detail}`);
    }
    out.push(
      "",
      "**Tools called:**",
      "",
      toolTrace(run),
      "",
      "**Answer:**",
      "",
      "```",
      truncate(run.answer || "(empty)", 1200),
      "```",
      "",
    );
    if (run.error) out.push(`**Turn error:** ${run.error}`, "");
  }

  return out.join("\n");
}
