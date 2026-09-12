import { describe, it, expect } from "vitest";
import {
  statusDotAppearance,
  loadFillColor,
  loadLabelColor,
  LOAD_LABEL_MIN_SUCCESS_PCT,
  statusDotRingColor,
  statusDotStyle,
} from "./statusDot";

describe("statusDotAppearance", () => {
  const starting = { state: "starting" as const, loadStartedAt: 1000, estLoadMs: 10000 };

  it("maps flat states without consulting the clock", () => {
    expect(statusDotAppearance({ ...starting, state: "ready" }, 0)).toEqual({ kind: "ready", pct: null });
    expect(statusDotAppearance({ ...starting, state: "stopping" }, 0)).toEqual({ kind: "stopping", pct: null });
    for (const state of ["stopped", "shutdown", "unknown"] as const) {
      expect(statusDotAppearance({ ...starting, state }, 0)).toEqual({ kind: "stopped", pct: null });
    }
  });

  it("is indeterminate while starting without an estimate (e.g. a peer or first load)", () => {
    expect(statusDotAppearance({ state: "starting", loadStartedAt: 1000 }, 6000)).toEqual({
      kind: "indeterminate",
      pct: null,
    });
    expect(statusDotAppearance({ ...starting, estLoadMs: 0 }, 6000)).toEqual({
      kind: "indeterminate",
      pct: null,
    });
  });

  it("fills by elapsed/estimate while under the estimate", () => {
    expect(statusDotAppearance(starting, 6000)).toEqual({ kind: "filling", pct: 50 });
    expect(statusDotAppearance(starting, 1000)).toEqual({ kind: "filling", pct: 0 });
  });

  it("becomes overrun at the 95% cap once the estimate is passed", () => {
    expect(statusDotAppearance(starting, 11500)).toEqual({ kind: "overrun", pct: 95 });
  });
});

describe("loadFillColor", () => {
  it("is pure warning at 0% and blends toward success with progress", () => {
    expect(loadFillColor(0)).toBe("color-mix(in oklch, var(--warning), var(--success) 0%)");
    expect(loadFillColor(50)).toBe("color-mix(in oklch, var(--warning), var(--success) 50%)");
  });

  it("clamps and rounds the mix percentage", () => {
    expect(loadFillColor(-10)).toContain(" 0%)");
    expect(loadFillColor(120)).toContain(" 95%)");
    expect(loadFillColor(33.4)).toContain(" 33%)");
  });
});

describe("loadLabelColor", () => {
  it("floors the mix so early text is never the least-legible amber", () => {
    // Below the floor, the label sits at the floor regardless of true progress,
    // so bar (pure amber at 0%) and text (floored) deliberately differ.
    expect(loadLabelColor(0)).toBe(loadFillColor(LOAD_LABEL_MIN_SUCCESS_PCT));
    expect(loadLabelColor(LOAD_LABEL_MIN_SUCCESS_PCT - 15)).toBe(
      loadFillColor(LOAD_LABEL_MIN_SUCCESS_PCT),
    );
  });

  it("tracks true progress once past the floor", () => {
    expect(loadLabelColor(LOAD_LABEL_MIN_SUCCESS_PCT + 20)).toBe(
      loadFillColor(LOAD_LABEL_MIN_SUCCESS_PCT + 20),
    );
    expect(loadLabelColor(95)).toBe(loadFillColor(95));
  });
});

describe("statusDotRingColor", () => {
  it("rings every loading state and nothing else", () => {
    expect(statusDotRingColor({ kind: "filling", pct: 50 })).toBe(loadFillColor(50));
    expect(statusDotRingColor({ kind: "indeterminate", pct: null })).toBe("var(--warning)");
    expect(statusDotRingColor({ kind: "overrun", pct: 95 })).toBe("var(--warning)");
    for (const kind of ["ready", "stopped", "stopping"] as const) {
      expect(statusDotRingColor({ kind, pct: null })).toBe("");
    }
  });

  it("warms the filling ring with progress, matching the pie", () => {
    expect(statusDotRingColor({ kind: "filling", pct: 0 })).toBe(loadFillColor(0));
    expect(statusDotRingColor({ kind: "filling", pct: 80 })).toBe(loadFillColor(80));
  });
});

describe("statusDotStyle", () => {
  it("returns no inline style for flat kinds", () => {
    expect(statusDotStyle({ kind: "ready", pct: null })).toBe("");
    expect(statusDotStyle({ kind: "stopped", pct: null })).toBe("");
    expect(statusDotStyle({ kind: "indeterminate", pct: null })).toBe("");
  });

  it("paints a conic pie in the blended colour while filling", () => {
    expect(statusDotStyle({ kind: "filling", pct: 50 })).toBe(
      "background-image: conic-gradient(color-mix(in oklch, var(--warning), var(--success) 50%) 50%, transparent 0)",
    );
  });

  it("keeps the sector in plain warning amber on overrun", () => {
    expect(statusDotStyle({ kind: "overrun", pct: 95 })).toBe(
      "background-image: conic-gradient(var(--warning) 95%, transparent 0)",
    );
  });
});
