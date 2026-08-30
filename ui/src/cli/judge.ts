import type { EvalCase } from "./cases";
import type { CaseResult } from "./grade";

/**
 * Stage-2 grading: an LLM judge over a *saved* results file.
 *
 * Two things follow from scoring a saved artifact rather than re-running the
 * agent. The agent model is never invoked again, so judging is cheap and
 * repeatable; and the same run can be re-judged with a different judge without
 * changing what is being judged, which is what makes judge-vs-judge
 * comparisons meaningful.
 *
 * The judge must be a larger model than the agent. A model grading its own
 * output ratifies its own mistakes, and the resulting score moves for reasons
 * that have nothing to do with the change under test.
 *
 * This does not reuse chatApi.ts: that module always sets stream: true, and a
 * judge wants one small blocking JSON reply.
 */

export interface JudgeVerdict {
  caseId: string;
  /** 0-5, as returned by the judge. */
  score: number;
  /** Per-rubric-item outcomes, index-aligned with the case's rubric. */
  rubric: { item: string; ok: boolean }[];
  rationale: string;
  error?: string;
}

export interface JudgeOptions {
  baseUrl: string;
  model: string;
  apiKey?: string;
  timeoutMs: number;
}

const SYSTEM = `You grade answers written by a documentation assistant for llama-swap, a proxy that swaps llama.cpp models on demand.

You are given the question, a reference answer, a rubric, and the answer under test. Grade the answer under test against the rubric only.

Rules:
- The answer does not have to match the reference wording. It has to be correct and to satisfy the rubric.
- An answer that invents a configuration key, flag or endpoint fails, however well written it is.
- An answer that correctly says the feature does not exist passes, when that is what the reference says.
- Extra correct detail is fine. Extra incorrect detail is not.

Reply with JSON only, no prose and no code fence:
{"rubric": [true, false, ...], "score": 0-5, "rationale": "one sentence"}
The "rubric" array must have exactly one boolean per rubric item, in order.`;

function buildUserMessage(c: EvalCase, answer: string): string {
  return [
    `## Question`,
    c.question,
    "",
    `## Reference answer`,
    c.reference ?? "(none supplied)",
    "",
    `## Rubric`,
    ...c.rubric.map((item, i) => `${i + 1}. ${item}`),
    "",
    `## Answer under test`,
    answer.trim() || "(the assistant produced no answer)",
  ].join("\n");
}

/** Pulls the JSON object out of a reply that may be fenced or padded with prose. */
export function extractJson(text: string): unknown {
  const fenced = /```(?:json)?\s*([\s\S]*?)```/i.exec(text);
  const candidate = fenced ? fenced[1] : text;
  const start = candidate.indexOf("{");
  const end = candidate.lastIndexOf("}");
  if (start === -1 || end === -1 || end < start) {
    throw new Error(`no JSON object in judge reply: ${text.slice(0, 200)}`);
  }
  return JSON.parse(candidate.slice(start, end + 1));
}

/** Normalises a judge reply into a verdict. Exported for tests. */
export function toVerdict(c: EvalCase, raw: unknown): JudgeVerdict {
  const obj = (raw ?? {}) as Record<string, unknown>;
  const flags = Array.isArray(obj.rubric) ? obj.rubric : [];
  const rubric = c.rubric.map((item, i) => ({ item, ok: flags[i] === true }));

  let score = typeof obj.score === "number" ? obj.score : NaN;
  if (!Number.isFinite(score)) {
    // A judge that returned rubric flags but no usable score still carries a
    // signal; derive one rather than throwing the whole verdict away.
    score = rubric.length ? (rubric.filter((r) => r.ok).length / rubric.length) * 5 : 0;
  }
  score = Math.max(0, Math.min(5, score));

  return {
    caseId: c.id,
    score,
    rubric,
    rationale: typeof obj.rationale === "string" ? obj.rationale : "",
  };
}

async function askJudge(prompt: string, opts: JudgeOptions): Promise<string> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), opts.timeoutMs);
  try {
    const headers: Record<string, string> = { "Content-Type": "application/json" };
    if (opts.apiKey) headers.Authorization = `Bearer ${opts.apiKey}`;

    const response = await fetch(new URL("/v1/chat/completions", opts.baseUrl).toString(), {
      method: "POST",
      headers,
      signal: controller.signal,
      body: JSON.stringify({
        model: opts.model,
        stream: false,
        temperature: 0,
        messages: [
          { role: "system", content: SYSTEM },
          { role: "user", content: prompt },
        ],
      }),
    });

    if (!response.ok) {
      throw new Error(`judge HTTP ${response.status}: ${(await response.text()).slice(0, 200)}`);
    }

    const body = (await response.json()) as { choices?: { message?: { content?: string } }[] };
    const content = body.choices?.[0]?.message?.content;
    if (typeof content !== "string") throw new Error("judge returned no message content");
    return content;
  } finally {
    clearTimeout(timer);
  }
}

/** Judges one case's best attempt. Cases with no rubric are skipped by the caller. */
export async function judgeCase(c: EvalCase, result: CaseResult, opts: JudgeOptions): Promise<JudgeVerdict> {
  // Judge the first attempt: judging every repeat multiplies cost for a
  // signal the deterministic flakiness number already reports.
  const answer = result.runs[0]?.answer ?? "";
  try {
    return toVerdict(c, extractJson(await askJudge(buildUserMessage(c, answer), opts)));
  } catch (error) {
    return {
      caseId: c.id,
      score: 0,
      rubric: c.rubric.map((item) => ({ item, ok: false })),
      rationale: "",
      error: error instanceof Error ? error.message : String(error),
    };
  }
}

export interface JudgeSummary {
  judged: number;
  /** Mean score on the 0-5 scale. */
  meanScore: number;
  /** Fraction of all rubric items satisfied. */
  rubricPassRate: number;
  errors: number;
}

export function summarizeJudge(verdicts: JudgeVerdict[]): JudgeSummary {
  const items = verdicts.flatMap((v) => v.rubric);
  return {
    judged: verdicts.length,
    meanScore: verdicts.length ? verdicts.reduce((sum, v) => sum + v.score, 0) / verdicts.length : 0,
    rubricPassRate: items.length ? items.filter((i) => i.ok).length / items.length : 0,
    errors: verdicts.filter((v) => v.error).length,
  };
}
