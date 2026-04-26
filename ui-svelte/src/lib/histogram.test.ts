import { describe, it, expect } from "vitest";
import { calculateHistogramData } from "./histogram";

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
