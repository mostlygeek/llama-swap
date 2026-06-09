import { describe, it, expect } from "vitest";
import { calculateHistogramData, calculateStackedHistogramData } from "./histogram";

describe("calculateHistogramData", () => {
  describe("edge cases", () => {
    it("returns null for empty input", () => {
      expect(calculateHistogramData([])).toBeNull();
    });

    it("handles single value", () => {
      const result = calculateHistogramData([42]);
      expect(result).not.toBeNull();
      expect(result!.bins).toEqual([1]);
      expect(result!.min).toBe(42);
      expect(result!.max).toBe(42);
      expect(result!.binSize).toBe(0);
      expect(result!.p50).toBe(42);
      expect(result!.p95).toBe(42);
      expect(result!.p99).toBe(42);
    });

    it("handles all identical values", () => {
      const result = calculateHistogramData([10, 10, 10, 10, 10]);
      expect(result).not.toBeNull();
      expect(result!.bins).toEqual([5]);
      expect(result!.min).toBe(10);
      expect(result!.max).toBe(10);
      expect(result!.binSize).toBe(0);
    });

    it("handles two distinct values", () => {
      const result = calculateHistogramData([10, 20]);
      expect(result).not.toBeNull();
      expect(result!.min).toBe(10);
      expect(result!.max).toBe(20);
      expect(result!.p50).toBe(15);
      const binSum = result!.bins.reduce((s, b) => s + b, 0);
      expect(binSum).toBe(2);
    });
  });

  describe("bin distribution", () => {
    it("bins sum to total number of values", () => {
      const values = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];
      const result = calculateHistogramData(values);
      expect(result).not.toBeNull();
      const binSum = result!.bins.reduce((s, b) => s + b, 0);
      expect(binSum).toBe(values.length);
    });

    it("distributes uniform values across bins", () => {
      const values = Array.from({ length: 100 }, (_, i) => i);
      const result = calculateHistogramData(values);
      expect(result).not.toBeNull();
      expect(result!.bins.length).toBe(8);
      const binSum = result!.bins.reduce((s, b) => s + b, 0);
      expect(binSum).toBe(100);
    });

    it("places values in correct bins", () => {
      const values = [1, 1, 1, 5, 5, 9, 9, 9];
      const result = calculateHistogramData(values, { minBins: 3, maxBins: 3 });
      expect(result).not.toBeNull();
      expect(result!.bins.length).toBe(3);
      expect(result!.bins.reduce((s, b) => s + b, 0)).toBe(8);
    });

    it("handles skewed distribution", () => {
      const values = [1, 1, 1, 1, 1, 100];
      const result = calculateHistogramData(values);
      expect(result).not.toBeNull();
      const binSum = result!.bins.reduce((s, b) => s + b, 0);
      expect(binSum).toBe(6);
    });
  });

  describe("percentiles", () => {
    it("calculates correct p50 for even-length array", () => {
      const values = [1, 2, 3, 4];
      const result = calculateHistogramData(values);
      expect(result).not.toBeNull();
      expect(result!.p50).toBe(2.5);
    });

    it("calculates correct p50 for odd-length array", () => {
      const values = [1, 2, 3, 4, 5];
      const result = calculateHistogramData(values);
      expect(result).not.toBeNull();
      expect(result!.p50).toBe(3);
    });

    it("calculates p99 with interpolation", () => {
      const values = Array.from({ length: 100 }, (_, i) => i + 1);
      const result = calculateHistogramData(values);
      expect(result).not.toBeNull();
      expect(result!.p99).toBeCloseTo(99.01);
    });

    it("calculates p95 with interpolation", () => {
      const values = Array.from({ length: 100 }, (_, i) => i + 1);
      const result = calculateHistogramData(values);
      expect(result).not.toBeNull();
      expect(result!.p95).toBeCloseTo(95.05);
    });

    it("percentiles are monotonically increasing", () => {
      const values = Array.from({ length: 200 }, () => Math.random() * 100);
      const result = calculateHistogramData(values);
      expect(result).not.toBeNull();
      expect(result!.p50).toBeLessThanOrEqual(result!.p95);
      expect(result!.p95).toBeLessThanOrEqual(result!.p99);
    });
  });

  describe("bin count adaptation", () => {
    it("uses minimum bins for small datasets", () => {
      // n=8: sturges=4, clamped up to minBins=5
      const values = Array.from({ length: 8 }, (_, i) => i);
      const result = calculateHistogramData(values);
      expect(result!.bins.length).toBe(5);
    });

    it("scales bins with dataset size", () => {
      // n=100: sturges=8
      const values = Array.from({ length: 100 }, (_, i) => i);
      const result = calculateHistogramData(values);
      expect(result!.bins.length).toBe(8);
    });

    it("caps bins at maximum", () => {
      // n=1000: sturges=11, clamped down to maxBins=10
      const values = Array.from({ length: 1000 }, (_, i) => i);
      const result = calculateHistogramData(values, { minBins: 5, maxBins: 10 });
      expect(result!.bins.length).toBe(10);
    });

    it("respects custom options", () => {
      // n=100: sturges=8, within [minBins=5, maxBins=10]
      const values = Array.from({ length: 100 }, (_, i) => i);
      const result = calculateHistogramData(values, { minBins: 5, maxBins: 10 });
      expect(result!.bins.length).toBe(8);
    });
  });

  describe("min and max", () => {
    it("correctly identifies min and max", () => {
      const values = [5, 3, 8, 1, 9, 2];
      const result = calculateHistogramData(values);
      expect(result!.min).toBe(1);
      expect(result!.max).toBe(9);
    });

    it("handles negative values", () => {
      const values = [-10, -5, 0, 5, 10];
      const result = calculateHistogramData(values);
      expect(result!.min).toBe(-10);
      expect(result!.max).toBe(10);
    });

    it("handles floating point values", () => {
      const values = [1.5, 2.7, 3.14, 0.5, 4.99];
      const result = calculateHistogramData(values);
      expect(result!.min).toBe(0.5);
      expect(result!.max).toBe(4.99);
    });
  });
});

describe("calculateStackedHistogramData", () => {
  describe("edge cases", () => {
    it("returns null for empty input", () => {
      expect(calculateStackedHistogramData([])).toBeNull();
    });

    it("handles single model", () => {
      const entries = [
        { model: "alpha", value: 10 },
        { model: "alpha", value: 20 },
        { model: "alpha", value: 30 },
      ];
      const result = calculateStackedHistogramData(entries);
      expect(result).not.toBeNull();
      expect(result!.models).toEqual(["alpha"]);
      const totalSegments = result!.bins.reduce((s, b) => s + b.totalCount, 0);
      expect(totalSegments).toBe(3);
    });

    it("handles single value per model", () => {
      const entries = [
        { model: "alpha", value: 42 },
        { model: "beta", value: 42 },
      ];
      const result = calculateStackedHistogramData(entries);
      expect(result).not.toBeNull();
      expect(result!.min).toBe(42);
      expect(result!.max).toBe(42);
      expect(result!.bins.length).toBe(1);
      expect(result!.bins[0].totalCount).toBe(2);
    });
  });

  describe("multi-model stacking", () => {
    it("groups values by model into segments", () => {
      const entries = [
        { model: "alpha", value: 10 },
        { model: "alpha", value: 15 },
        { model: "beta", value: 20 },
        { model: "beta", value: 25 },
        { model: "beta", value: 30 },
      ];
      const result = calculateStackedHistogramData(entries);
      expect(result).not.toBeNull();
      expect(result!.models).toEqual(["alpha", "beta"]);
      const totalSegments = result!.bins.reduce((s, b) => s + b.totalCount, 0);
      expect(totalSegments).toBe(5);
    });

    it("segment counts sum to total entries", () => {
      const entries = [
        { model: "a", value: 1 },
        { model: "a", value: 2 },
        { model: "b", value: 3 },
        { model: "b", value: 4 },
        { model: "c", value: 5 },
      ];
      const result = calculateStackedHistogramData(entries);
      expect(result).not.toBeNull();
      const totalSegments = result!.bins.reduce((s, b) => s + b.totalCount, 0);
      expect(totalSegments).toBe(5);
    });

    it("models are sorted alphabetically", () => {
      const entries = [
        { model: "zebra", value: 10 },
        { model: "alpha", value: 20 },
        { model: "middle", value: 30 },
      ];
      const result = calculateStackedHistogramData(entries);
      expect(result!.models).toEqual(["alpha", "middle", "zebra"]);
    });

    it("segments within bins are sorted by model", () => {
      const entries = [
        { model: "beta", value: 10 },
        { model: "alpha", value: 10 },
      ];
      const result = calculateStackedHistogramData(entries);
      const firstBin = result!.bins[0];
      const modelNames = firstBin.segments.map((s) => s.model);
      expect(modelNames).toEqual(["alpha", "beta"]);
    });

    it("omits zero-count segments from bins", () => {
      const entries = [
        { model: "alpha", value: 1 },
        { model: "beta", value: 100 },
      ];
      const result = calculateStackedHistogramData(entries, { minBins: 3, maxBins: 3 });
      expect(result).not.toBeNull();
      // The first bin should only contain alpha (value=1)
      const firstBin = result!.bins[0];
      const modelsInFirstBin = firstBin.segments.map((s) => s.model);
      expect(modelsInFirstBin).toEqual(["alpha"]);
      // The last bin should only contain beta (value=100)
      const lastBin = result!.bins[2];
      const modelsInLastBin = lastBin.segments.map((s) => s.model);
      expect(modelsInLastBin).toEqual(["beta"]);
    });
  });

  describe("bin properties", () => {
    it("each bin has correct start and end", () => {
      const entries = [
        { model: "a", value: 0 },
        { model: "a", value: 10 },
      ];
      const result = calculateStackedHistogramData(entries, { minBins: 2, maxBins: 2 });
      expect(result!.bins.length).toBe(2);
      expect(result!.bins[0].binStart).toBe(0);
      expect(result!.bins[0].binEnd).toBeCloseTo(5);
      expect(result!.bins[1].binStart).toBeCloseTo(5);
      expect(result!.bins[1].binEnd).toBe(10);
    });

    it("totalCount equals sum of segment counts", () => {
      const entries = [
        { model: "a", value: 1 },
        { model: "a", value: 2 },
        { model: "b", value: 3 },
        { model: "b", value: 4 },
        { model: "b", value: 5 },
      ];
      const result = calculateStackedHistogramData(entries);
      for (const bin of result!.bins) {
        const segmentSum = bin.segments.reduce((s, seg) => s + seg.count, 0);
        expect(bin.totalCount).toBe(segmentSum);
      }
    });
  });

  describe("percentiles", () => {
    it("computes percentiles from combined distribution", () => {
      const entries = [
        { model: "a", value: 1 },
        { model: "a", value: 2 },
        { model: "b", value: 3 },
        { model: "b", value: 4 },
      ];
      const result = calculateStackedHistogramData(entries);
      // Same as flat histogram of [1,2,3,4]
      expect(result!.p50).toBe(2.5);
    });

    it("percentiles are monotonically increasing", () => {
      const entries: { model: string; value: number }[] = [];
      for (let i = 0; i < 100; i++) {
        entries.push({ model: i % 2 === 0 ? "a" : "b", value: Math.random() * 100 });
      }
      const result = calculateStackedHistogramData(entries);
      expect(result!.p50).toBeLessThanOrEqual(result!.p95);
      expect(result!.p95).toBeLessThanOrEqual(result!.p99);
    });
  });
});
