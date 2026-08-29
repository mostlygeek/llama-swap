import { readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import type { SuiteSummary, CaseResult } from "./grade";

/**
 * A running log of every eval, kept alongside the other eval artifacts.
 *
 * The optimization loop is long and spans many sessions; without a durable
 * record of what each attempt scored, deciding "has this stopped improving?"
 * means re-reading run JSON files. Keeping it with those files makes a run's
 * artifacts self-contained.
 *
 * Markdown rather than CSV because the numbers alone do not say what happened.
 * A run that scores 94% by refusing to use tools and a run that scores 94% by
 * answering well are the same row and very different results, so each entry
 * carries a short narrative generated from its own failures.
 */

const HERE = path.dirname(fileURLToPath(import.meta.url));
export const DEFAULT_HISTORY = path.resolve(
  HERE,
  "../../../evals/docs-agent/runs/history.md",
);

const TITLE = "# llama-swap Docs Agent — eval history";

const PREAMBLE = `One row per run of \`evals/docs-agent/run.sh\`, newest last. Written automatically;
edit the notes if you like, but leave the table alone — it is parsed back to
decide when tuning has stopped paying off.

Pass rate is the mean over cases, so every question counts the same however
many times it was repeated. "Clean" counts questions that passed on *every*
attempt.`;

const TABLE_HEADER = [
  "| # | date (UTC) | label | model | set | cases | runs | pass rate | clean | flaky | steps | speed |",
  "| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |",
];

const RUNS_HEADING = "## What happened in each run";

export interface HistoryEntry {
  timestamp: string;
  label: string;
  repeat: number;
  model: string;
  casesDir: string;
  promptSource: string;
  summary: SuiteSummary;
  results: CaseResult[];
  /** The --only filter, when the run covered a subset of the case set. */
  only?: string;
  /** Explicitly selected groups, sorted for a stable comparison key. */
  groups?: string[];
}

/** One row as read back from the log. Only what the table and stop rule need. */
export interface HistoryRow {
  index: number;
  date: string;
  label: string;
  model: string;
  set: string;
  cases: number;
  repeat: number;
  passRate: number;
  clean: string;
  flaky: number;
  steps: string;
  speed: string;
}

function formatDate(iso: string): string {
  return iso.replace("T", " ").slice(0, 16);
}

/**
 * The case set a run is comparable against.
 *
 * A run filtered with --only covers a different set of questions, so its score
 * is not comparable to a full run and must never set the best-so-far: a
 * five-case subset scoring 100% would otherwise make every real run look like
 * a regression for good. Marking it here scopes it out of both the stopping
 * rule and the run-over-run comparison, while keeping it in the record.
 */
export function setName(
  casesDir: string,
  only?: string,
  groups?: string[],
): string {
  const base = path.basename(casesDir);
  const groupLabel = groups?.length ? ` [${[...groups].sort().join(",")}]` : "";
  return only ? `${base}${groupLabel} (subset)` : `${base}${groupLabel}`;
}

export function toRow(entry: HistoryEntry, index: number): HistoryRow {
  const s = entry.summary;
  return {
    index,
    date: formatDate(entry.timestamp),
    label: entry.label,
    model: entry.model,
    set: setName(entry.casesDir, entry.only, entry.groups),
    cases: s.cases,
    repeat: entry.repeat,
    passRate: s.passRate,
    clean: `${s.fullyPassing}/${s.cases}`,
    flaky: s.flaky,
    steps: s.meanIterations.toFixed(2),
    speed: `${(s.meanDurationMs / 1000).toFixed(1)}s`,
  };
}

export function renderRow(row: HistoryRow): string {
  return `| ${[
    row.index,
    row.date,
    row.label,
    `\`${row.model}\``,
    row.set,
    row.cases,
    `×${row.repeat}`,
    `**${(row.passRate * 100).toFixed(1)}%**`,
    row.clean,
    row.flaky,
    row.steps,
    row.speed,
  ].join(" | ")} |`;
}

export function parseRow(line: string): HistoryRow | null {
  if (!line.trim().startsWith("|")) return null;
  const cells = line
    .trim()
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split("|")
    .map((c) => c.trim());
  if (cells.length < 12) return null;

  const index = Number(cells[0]);
  const passRate = Number(cells[7].replace(/[*%]/g, "")) / 100;
  if (!Number.isFinite(index) || !Number.isFinite(passRate)) return null;

  return {
    index,
    date: cells[1],
    label: cells[2],
    model: cells[3].replace(/`/g, ""),
    set: cells[4],
    cases: Number(cells[5]),
    repeat: Number(cells[6].replace("×", "")),
    passRate,
    clean: cells[8],
    flaky: Number(cells[9]),
    steps: cells[10],
    speed: cells[11],
  };
}

function plural(n: number, one: string, many = `${one}s`): string {
  return `${n} ${n === 1 ? one : many}`;
}

/**
 * A plain-English account of one run, built from its own results.
 *
 * Deliberately mechanical: it reports what the numbers and the failing cases
 * say and nothing more, so an entry written months ago can still be trusted.
 */
export function renderNarrative(
  entry: HistoryEntry,
  index: number,
  previous?: HistoryRow,
): string {
  const s = entry.summary;
  const failing = entry.results.filter((r) => r.passCount < r.attempts);
  const out: string[] = [];

  out.push(
    `### ${index}. \`${entry.label}\` — ${entry.model} — ${(s.passRate * 100).toFixed(1)}%`,
    "",
  );

  const headline =
    `Answered **${s.fullyPassing} of ${s.cases}** documentation questions correctly` +
    `, taking ${(s.meanDurationMs / 1000).toFixed(1)}s and ${s.meanIterations.toFixed(1)} steps per question.`;
  out.push(headline);

  if (previous) {
    const delta = (s.passRate - previous.passRate) * 100;
    const comparison =
      Math.abs(delta) < 0.05
        ? "level with"
        : `${Math.abs(delta).toFixed(1)} points ${delta > 0 ? "better than" : "worse than"}`;
    out.push(
      `That is ${comparison} the previous run (\`${previous.label}\`, ${(previous.passRate * 100).toFixed(1)}%` +
        `${previous.model === entry.model ? "" : ` on ${previous.model}`}).`,
    );
  }
  out.push("");

  // How it used the tools. A model that answers from memory can score well and
  // still be the wrong thing to ship, so this gets its own line.
  const notes: string[] = [];
  if (s.noToolAnswers === 0) {
    notes.push("It looked something up before every answer.");
  } else {
    notes.push(
      `It answered ${plural(s.noToolAnswers, "question")} straight from memory without consulting the docs.`,
    );
  }
  if (s.toolErrors > 0) {
    notes.push(
      `${s.toolErrors} of its ${s.toolCalls} tool calls came back as errors.`,
    );
  }
  if (s.errors > 0) {
    notes.push(`${plural(s.errors, "turn")} failed outright.`);
  }
  if (s.flaky > 0) {
    notes.push(
      `${plural(s.flaky, "question")} passed on some attempts and failed on others.`,
    );
  }
  out.push(notes.join(" "), "");

  if (!failing.length) {
    out.push("Nothing failed.", "");
    return out.join("\n");
  }

  // The abstention group is the one worth calling out by name: inventing a
  // config key is the failure that looks most like a good answer.
  const invented = failing.filter((r) => r.tags.includes("negative"));
  if (invented.length) {
    out.push(
      `**Made things up.** In ${plural(invented.length, "case")} it described configuration that does not exist ` +
        `rather than saying so: ${invented.map((r) => `\`${r.caseId}\``).join(", ")}.`,
      "",
    );
  }

  const others = failing.filter((r) => !r.tags.includes("negative"));
  if (others.length) {
    out.push(
      `**Got wrong.** ${others.map((r) => `\`${r.caseId}\``).join(", ")}.`,
      "",
    );
  }

  return out.join("\n");
}

/** Appends one run, rewriting the file so the table stays in one piece. */
export async function recordRun(
  file: string,
  entry: HistoryEntry,
): Promise<HistoryRow[]> {
  let existingTable = "";
  let existingNarratives = "";
  try {
    const text = await readFile(file, "utf8");
    const split = text.indexOf(RUNS_HEADING);
    existingTable = split === -1 ? text : text.slice(0, split);
    existingNarratives =
      split === -1 ? "" : text.slice(split + RUNS_HEADING.length).trim();
  } catch {
    // First run; both halves stay empty.
  }

  const rows = existingTable
    .split("\n")
    .map(parseRow)
    .filter((r): r is HistoryRow => r !== null);

  // Compare like with like: the previous run of the *same* set. A holdout or
  // subset run in between is not a meaningful baseline for this one.
  const set = setName(entry.casesDir, entry.only, entry.groups);
  const sameSet = rows.filter((r) => r.set === set);
  const previous = sameSet.length ? sameSet[sameSet.length - 1] : undefined;
  const index = rows.length + 1;
  const row = toRow(entry, index);
  const all = [...rows, row];

  const body = [
    TITLE,
    "",
    PREAMBLE,
    "",
    ...TABLE_HEADER,
    ...all.map(renderRow),
    "",
    RUNS_HEADING,
    "",
    existingNarratives ? `${existingNarratives}\n` : "",
    renderNarrative(entry, index, previous),
  ].join("\n");

  await writeFile(file, `${body.replace(/\n{3,}/g, "\n\n").trimEnd()}\n`);
  return all;
}

export async function readHistory(file: string): Promise<HistoryRow[]> {
  let text: string;
  try {
    text = await readFile(file, "utf8");
  } catch {
    return [];
  }
  const split = text.indexOf(RUNS_HEADING);
  return (split === -1 ? text : text.slice(0, split))
    .split("\n")
    .map(parseRow)
    .filter((r): r is HistoryRow => r !== null);
}

export interface StopVerdict {
  sinceBest: number;
  best: number;
  bestLabel: string;
  shouldStop: boolean;
}

/**
 * The diminishing-returns rule: stop after `limit` consecutive runs that fail
 * to beat the best score seen for this case set.
 *
 * Comparisons are scoped to one case set, because a holdout run scores
 * differently by construction and would otherwise look like a regression and
 * reset or trip the counter.
 */
export function evaluateStop(
  rows: HistoryRow[],
  casesDir: string,
  limit = 5,
  only?: string,
  groups?: string[],
): StopVerdict {
  const scoped = rows.filter((r) => r.set === setName(casesDir, only, groups));
  if (!scoped.length)
    return { sinceBest: 0, best: 0, bestLabel: "", shouldStop: false };

  let best = -Infinity;
  let bestLabel = "";
  let sinceBest = 0;

  for (const row of scoped) {
    // Strictly greater: a tie is not an improvement, it is another attempt
    // that did not move the number.
    if (row.passRate > best) {
      best = row.passRate;
      bestLabel = row.label;
      sinceBest = 0;
    } else {
      sinceBest++;
    }
  }

  return { sinceBest, best, bestLabel, shouldStop: sinceBest >= limit };
}
