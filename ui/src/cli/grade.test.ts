import { describe, it, expect } from "vitest";
import { parseCase } from "./cases";
import { gradeRun, summarizeCase, summarizeSuite, type RunRecord } from "./grade";

function makeCase(overrides: Record<string, unknown> = {}) {
  return parseCase({ id: "c1", question: "q?", expect_regex: ["ttl"], ...overrides }, "f.yaml", 0);
}

function makeRun(overrides: Partial<RunRecord> = {}): RunRecord {
  return {
    caseId: "c1",
    attempt: 1,
    answer: "set ttl: 300",
    reasoning: "",
    toolCalls: [{ name: "docs__search_docs", args: "{}", ok: true, durationMs: 5 }],
    iterations: 2,
    doneReason: "stop",
    durationMs: 1000,
    ...overrides,
  };
}

describe("gradeRun", () => {
  it("passes when every assertion holds", () => {
    expect(gradeRun(makeCase(), makeRun()).passed).toBe(true);
  });

  it("matches expect_regex case-insensitively", () => {
    expect(gradeRun(makeCase({ expect_regex: ["TTL"] }), makeRun()).passed).toBe(true);
  });

  it("fails when a required pattern is missing", () => {
    const graded = gradeRun(makeCase({ expect_regex: ["globalTTL"] }), makeRun());
    expect(graded.passed).toBe(false);
    expect(graded.assertions.find((a) => !a.ok)?.kind).toBe("expect_regex");
  });

  it("expect_any needs only one match", () => {
    const c = makeCase({ expect_any: ["nope", "300"] });
    expect(gradeRun(c, makeRun()).passed).toBe(true);
    expect(gradeRun(makeCase({ expect_any: ["nope", "nada"] }), makeRun()).passed).toBe(false);
  });

  it("forbid_regex fails on a match", () => {
    expect(gradeRun(makeCase({ forbid_regex: ["300"] }), makeRun()).passed).toBe(false);
  });

  it("checks tools called and not called", () => {
    expect(gradeRun(makeCase({ expect_tools: ["docs__get_doc"] }), makeRun()).passed).toBe(false);
    expect(gradeRun(makeCase({ forbid_tools: ["docs__search_docs"] }), makeRun()).passed).toBe(false);
    expect(gradeRun(makeCase({ expect_tools: ["docs__search_docs"] }), makeRun()).passed).toBe(true);
  });

  it("expect_tools_any needs only one of the named tools", () => {
    const c = makeCase({ expect_tools_any: ["docs__get_config_schema", "docs__search_docs"] });
    expect(gradeRun(c, makeRun()).passed).toBe(true);
    const other = makeCase({ expect_tools_any: ["docs__get_doc", "docs__list_docs"] });
    expect(gradeRun(other, makeRun()).passed).toBe(false);
  });

  it("require_tools catches an answer from memory", () => {
    const c = makeCase({ require_tools: true });
    expect(gradeRun(c, makeRun({ toolCalls: [] })).passed).toBe(false);
  });

  it("enforces max_iterations", () => {
    expect(gradeRun(makeCase({ max_iterations: 1 }), makeRun()).passed).toBe(false);
    expect(gradeRun(makeCase({ max_iterations: 2 }), makeRun()).passed).toBe(true);
  });

  // A turn that never finished did not answer, whatever text it left behind.
  it("fails a turn that hit the iteration ceiling even if the text matches", () => {
    const graded = gradeRun(makeCase(), makeRun({ doneReason: "max_iterations" }));
    expect(graded.passed).toBe(false);
    expect(graded.assertions.find((a) => !a.ok)?.kind).toBe("completed");
  });

  it("fails a turn that errored", () => {
    expect(gradeRun(makeCase(), makeRun({ error: "boom" })).passed).toBe(false);
  });
});

describe("summarize", () => {
  it("reports a flaky case", () => {
    const c = makeCase();
    const runs = [gradeRun(c, makeRun()), gradeRun(c, makeRun({ answer: "no match here" }))];
    const result = summarizeCase(c, runs);
    expect(result.passCount).toBe(1);
    expect(result.passRate).toBe(0.5);
    expect(result.flaky).toBe(true);
  });

  it("weights every case equally regardless of repeat count", () => {
    const c = makeCase();
    const clean = summarizeCase(c, [gradeRun(c, makeRun())]);
    const half = summarizeCase(makeCase({ id: "c2" }), [
      gradeRun(c, makeRun()),
      gradeRun(c, makeRun({ answer: "nope" })),
    ]);
    const summary = summarizeSuite([clean, half]);
    expect(summary.passRate).toBeCloseTo(0.75);
    expect(summary.fullyPassing).toBe(1);
    expect(summary.flaky).toBe(1);
  });

  it("counts tool errors and no-tool answers", () => {
    const c = makeCase();
    const summary = summarizeSuite([
      summarizeCase(c, [
        gradeRun(c, makeRun({ toolCalls: [{ name: "docs__search_docs", args: "{}", ok: false, durationMs: 1 }] })),
        gradeRun(c, makeRun({ toolCalls: [] })),
      ]),
    ]);
    expect(summary.toolErrors).toBe(1);
    expect(summary.noToolAnswers).toBe(1);
  });
});
