import type { EvalCase } from "./cases";

/**
 * Stage-1 grading: deterministic assertions over one agent turn.
 *
 * This is the number the optimization loop steers by, so it is regex and set
 * membership only -- no model in the loop, no variance from the scorer itself.
 * The LLM judge in judge.ts is a separate, later pass.
 */

/** One tool invocation, as recorded from the agent event stream. */
export interface ToolInvocation {
  name: string;
  args: string;
  ok: boolean;
  durationMs: number;
}

/** Everything one run of one case produced. */
export interface RunRecord {
  caseId: string;
  attempt: number;
  answer: string;
  reasoning: string;
  toolCalls: ToolInvocation[];
  iterations: number;
  /** The agent loop's own termination reason: stop | max_iterations | aborted | error. */
  doneReason: string;
  durationMs: number;
  error?: string;
}

export interface Assertion {
  kind: string;
  detail: string;
  ok: boolean;
}

export interface GradedRun extends RunRecord {
  assertions: Assertion[];
  passed: boolean;
}

/** Case-insensitive by default: models vary wildly on capitalisation. */
function matches(pattern: string, text: string): boolean {
  return new RegExp(pattern, "i").test(text);
}

export function gradeRun(c: EvalCase, run: RunRecord): GradedRun {
  const assertions: Assertion[] = [];
  const called = new Set(run.toolCalls.map((t) => t.name));

  // A turn that errored or was cut short did not answer the question, whatever
  // the text happens to contain.
  if (run.error) {
    assertions.push({ kind: "no_error", detail: run.error, ok: false });
  }
  if (run.doneReason === "max_iterations") {
    assertions.push({
      kind: "completed",
      detail: `hit the ${run.iterations}-iteration ceiling without answering`,
      ok: false,
    });
  }

  for (const pattern of c.expectRegex) {
    assertions.push({ kind: "expect_regex", detail: pattern, ok: matches(pattern, run.answer) });
  }

  if (c.expectAny.length) {
    const ok = c.expectAny.some((pattern) => matches(pattern, run.answer));
    assertions.push({ kind: "expect_any", detail: c.expectAny.join(" | "), ok });
  }

  for (const pattern of c.forbidRegex) {
    assertions.push({ kind: "forbid_regex", detail: pattern, ok: !matches(pattern, run.answer) });
  }

  for (const name of c.expectTools) {
    assertions.push({ kind: "expect_tool", detail: name, ok: called.has(name) });
  }

  // Several questions are legitimately answerable through more than one tool --
  // "what is this key's default" is as well served by get_config_schema as by
  // search_docs -- and pinning such a case to one tool measures conformity
  // rather than accuracy.
  if (c.expectToolsAny.length) {
    assertions.push({
      kind: "expect_tools_any",
      detail: c.expectToolsAny.join(" | "),
      ok: c.expectToolsAny.some((name) => called.has(name)),
    });
  }

  for (const name of c.forbidTools) {
    assertions.push({ kind: "forbid_tool", detail: name, ok: !called.has(name) });
  }

  if (c.requireTools) {
    assertions.push({
      kind: "require_tools",
      detail: "answered without calling any tool",
      ok: run.toolCalls.length > 0,
    });
  }

  if (c.maxIterations !== undefined) {
    assertions.push({
      kind: "max_iterations",
      detail: `${run.iterations} <= ${c.maxIterations}`,
      ok: run.iterations <= c.maxIterations,
    });
  }

  return { ...run, assertions, passed: assertions.every((a) => a.ok) };
}

export interface CaseResult {
  caseId: string;
  tags: string[];
  question: string;
  runs: GradedRun[];
  /** How many of the attempts passed. */
  passCount: number;
  attempts: number;
  /** passCount / attempts. A value strictly between 0 and 1 means the case is flaky. */
  passRate: number;
  flaky: boolean;
}

export function summarizeCase(c: EvalCase, runs: GradedRun[]): CaseResult {
  const passCount = runs.filter((r) => r.passed).length;
  return {
    caseId: c.id,
    tags: c.tags,
    question: c.question,
    runs,
    passCount,
    attempts: runs.length,
    passRate: runs.length ? passCount / runs.length : 0,
    flaky: passCount > 0 && passCount < runs.length,
  };
}

export interface SuiteSummary {
  cases: number;
  attempts: number;
  /** Mean of the per-case pass rates -- every case weighs the same regardless of --repeat. */
  passRate: number;
  /** Cases that passed every attempt. The strict, headline number. */
  fullyPassing: number;
  flaky: number;
  meanIterations: number;
  meanDurationMs: number;
  toolCalls: number;
  toolErrors: number;
  noToolAnswers: number;
  errors: number;
}

export function summarizeSuite(results: CaseResult[]): SuiteSummary {
  const runs = results.flatMap((r) => r.runs);
  const n = runs.length || 1;
  const toolCalls = runs.flatMap((r) => r.toolCalls);

  return {
    cases: results.length,
    attempts: runs.length,
    passRate: results.length ? results.reduce((sum, r) => sum + r.passRate, 0) / results.length : 0,
    fullyPassing: results.filter((r) => r.passCount === r.attempts).length,
    flaky: results.filter((r) => r.flaky).length,
    meanIterations: runs.reduce((sum, r) => sum + r.iterations, 0) / n,
    meanDurationMs: runs.reduce((sum, r) => sum + r.durationMs, 0) / n,
    toolCalls: toolCalls.length,
    toolErrors: toolCalls.filter((t) => !t.ok).length,
    noToolAnswers: runs.filter((r) => r.toolCalls.length === 0).length,
    errors: runs.filter((r) => r.error).length,
  };
}
