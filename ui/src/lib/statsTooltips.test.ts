import { describe, expect, it } from "vitest";
import { phaseValueTooltip, type PhaseKind, type PhaseMetric } from "./statsTooltips";

const kinds: PhaseKind[] = ["prompt", "reasoning", "response", "generation"];
const metrics: PhaseMetric[] = ["tokens", "time", "speed", "perToken"];

describe("statsTooltips", () => {
  it("has a description for every value", () => {
    for (const kind of kinds) {
      for (const metric of metrics) {
        const exact = phaseValueTooltip(kind, metric, { approxTokens: false, approxTimings: false });
        expect(exact.length, `${kind}/${metric}`).toBeGreaterThan(20);
        expect(exact, `${kind}/${metric}`).not.toContain("~");
      }
    }
  });

  it("explains the ~ on estimated values", () => {
    const approx = { approxTokens: true, approxTimings: true };
    expect(phaseValueTooltip("reasoning", "tokens", approx)).toMatch(/~ Estimated.*split/);
    expect(phaseValueTooltip("generation", "tokens", approx)).toMatch(/~ Estimated from streamed chunks/);
    expect(phaseValueTooltip("prompt", "time", approx)).toMatch(/first token.*browser/);
    expect(phaseValueTooltip("response", "speed", approx)).toMatch(/~ Derived/);
  });
});
