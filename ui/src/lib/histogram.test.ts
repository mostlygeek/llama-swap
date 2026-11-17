import { describe, it, expect } from "vitest";
import { formatOutlierNote, visiblePercentiles } from "./histogram";
import type { HistogramData } from "./types";

function histogram(overrides: Partial<HistogramData> = {}): HistogramData {
  return {
    bins: [1, 2, 3],
    min: 10,
    max: 100,
    binSize: 30,
    p50: 40,
    p95: 80,
    p99: 95,
    outliers: 0,
    ...overrides,
  };
}

describe("visiblePercentiles", () => {
  it("keeps markers inside the binned range", () => {
    expect(visiblePercentiles(histogram())).toEqual([
      { key: "p50", value: 40 },
      { key: "p95", value: 80 },
      { key: "p99", value: 95 },
    ]);
  });

  it("drops markers above max and below min", () => {
    const markers = visiblePercentiles(histogram({ min: 20, max: 60, p50: 15, p95: 50, p99: 90 }));
    expect(markers).toEqual([{ key: "p95", value: 50 }]);
  });

  it("drops every marker when all values share one bucket", () => {
    expect(visiblePercentiles(histogram({ min: 7, max: 7, p50: 7, p95: 7, p99: 7 }))).toEqual([]);
  });
});

describe("formatOutlierNote", () => {
  it("stays silent when nothing is hidden", () => {
    expect(formatOutlierNote(0)).toBeNull();
  });

  it("uses the singular for one outlier", () => {
    expect(formatOutlierNote(1)).toBe("1 outlier hidden");
  });

  it("uses the plural for several outliers", () => {
    expect(formatOutlierNote(12)).toBe("12 outliers hidden");
  });
});
