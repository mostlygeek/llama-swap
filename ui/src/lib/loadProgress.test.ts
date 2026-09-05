import { describe, it, expect } from "vitest";
import { computeLoadProgress, formatLoadProgressLabel, formatRemainingLabel } from "./loadProgress";

describe("computeLoadProgress", () => {
  const baseModel = {
    state: "starting" as const,
    loadStartedAt: 1000,
    estLoadMs: 10000,
  };

  it("returns null for non-starting states", () => {
    const states: Array<Parameters<typeof computeLoadProgress>[0]["state"]> = [
      "ready",
      "stopped",
      "stopping",
      "shutdown",
      "unknown",
    ];
    for (const state of states) {
      expect(
        computeLoadProgress({ ...baseModel, state }, 5000),
      ).toBeNull();
    }
  });

  it("returns null when state is undefined", () => {
    // Test with an object that has no state property at all
    expect(
      computeLoadProgress({ loadStartedAt: 1000, estLoadMs: 10000 } as any, 5000),
    ).toBeNull();
  });

  it("handles indeterminate when estLoadMs is undefined", () => {
    const result = computeLoadProgress(
      { ...baseModel, estLoadMs: undefined },
      6000,
    );
    expect(result).toEqual({
      mode: "indeterminate",
      elapsedMs: 5000,
      pct: null,
    });
  });

  it("handles indeterminate when estLoadMs is 0", () => {
    const result = computeLoadProgress(
      { ...baseModel, estLoadMs: 0 },
      6000,
    );
    expect(result).toEqual({
      mode: "indeterminate",
      elapsedMs: 5000,
      pct: null,
    });
  });

  it("computes estimated progress correctly", () => {
    // elapsed = 6000 - 1000 = 5000, ratio = 0.5, pct = 50, eta = 5000
    const result = computeLoadProgress(baseModel, 6000);
    expect(result).toEqual({
      mode: "estimated",
      elapsedMs: 5000,
      pct: 50,
      etaMs: 5000,
    });
  });

  it("clamps at 95% when ratio is between 0.95 and 1", () => {
    // elapsed = 9700, ratio = 0.97 -> round(97) actually exceeds the 95 cap,
    // so this exercises the Math.min clamp (unlike a ratio of exactly 0.95,
    // where round(95) makes the clamp a no-op).
    const result = computeLoadProgress(baseModel, 10700);
    expect(result).toEqual({
      mode: "estimated",
      elapsedMs: 9700,
      pct: 95,
      etaMs: 300,
    });
  });

  it("treats ratio exactly 1 as overrun, not estimated", () => {
    // elapsed = 10000 == estLoadMs, ratio = 1.0 -> the `ratio < 1` branch is
    // false, so this is the overrun boundary.
    const result = computeLoadProgress(baseModel, 11000);
    expect(result).toEqual({
      mode: "overrun",
      elapsedMs: 10000,
      pct: 95,
      etaMs: 0,
    });
  });

  it("clamps at 95% when ratio > 1 (overrun)", () => {
    // elapsed = 10500, ratio = 1.05, mode = overrun, pct = 95, eta = 0
    const result = computeLoadProgress(baseModel, 11500);
    expect(result).toEqual({
      mode: "overrun",
      elapsedMs: 10500,
      pct: 95,
      etaMs: 0,
    });
  });

  it("handles negative skew (now < loadStartedAt)", () => {
    const result = computeLoadProgress(baseModel, 500);
    expect(result).toEqual({
      mode: "estimated",
      elapsedMs: 0,
      pct: 0,
      etaMs: 10000,
    });
  });

  it("handles now === loadStartedAt", () => {
    const result = computeLoadProgress(baseModel, 1000);
    expect(result).toEqual({
      mode: "estimated",
      elapsedMs: 0,
      pct: 0,
      etaMs: 10000,
    });
  });
});

describe("formatRemainingLabel", () => {
  it("renders whole seconds", () => {
    expect(formatRemainingLabel(5000)).toBe("~5s left");
  });

  it("rounds up so it never promises the load early", () => {
    expect(formatRemainingLabel(4400)).toBe("~5s left");
    expect(formatRemainingLabel(300)).toBe("~1s left");
  });

  it("floors at 1s instead of showing ~0s", () => {
    expect(formatRemainingLabel(0)).toBe("~1s left");
    expect(formatRemainingLabel(-50)).toBe("~1s left");
  });
});

describe("formatLoadProgressLabel", () => {
  it("formats indeterminate label", () => {
    const label = formatLoadProgressLabel({
      mode: "indeterminate",
      elapsedMs: 5500,
      pct: null,
    });
    expect(label).toBe("Loading… 5.5s");
  });

  it("formats estimated label as elapsed plus time remaining (seconds, not raw ms)", () => {
    const label = formatLoadProgressLabel({
      mode: "estimated",
      elapsedMs: 5000,
      pct: 50,
      etaMs: 5000,
    });
    // Pinning the exact string catches a unit regression
    // (e.g. "5000.0s · ~5000s left") a loose regex misses.
    expect(label).toBe("Loading… 5.0s · ~5s left");
  });

  it("formats overrun label without a remaining estimate", () => {
    const label = formatLoadProgressLabel({
      mode: "overrun",
      elapsedMs: 10500,
      pct: 95,
      etaMs: 0,
    });
    expect(label).toBe("Taking longer than usual… 10.5s");
  });
});
