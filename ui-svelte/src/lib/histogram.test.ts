import { describe, it, expect } from "vitest";
import { buildHistogramData } from "./histogram";
import type { Metrics } from "./types";

function makeMetric(tokens_per_second: number, overrides: Partial<Metrics> = {}): Metrics {
  return {
    id: 0,
    timestamp: "2024-01-01T00:00:00Z",
    model: "test",
    cache_tokens: 0,
    input_tokens: 10,
    output_tokens: 100,
    prompt_per_second: 50,
    tokens_per_second,
    duration_ms: 5000,
    has_capture: false,
    ...overrides,
  };
}

describe("buildHistogramData", () => {
  it("returns null for empty metrics", () => {
    expect(buildHistogramData([])).toBeNull();
  });

  it("returns null when all metrics have tokens_per_second < 0", () => {
    const metrics = [makeMetric(-1), makeMetric(-1)];
    expect(buildHistogramData(metrics)).toBeNull();
  });

  it("filters out metrics with tokens_per_second < 0", () => {
    const metrics = [makeMetric(-1), makeMetric(25), makeMetric(30)];
    const result = buildHistogramData(metrics);
    expect(result).not.toBeNull();
    // total count across bins must equal the 2 valid metrics
    const total = result!.bins.reduce((a, b) => a + b, 0);
    expect(total).toBe(2);
  });

  it("uses tokens_per_second, not output_tokens/duration_ms", () => {
    // High tokens_per_second but low output_tokens/duration would be ~2 t/s if computed wrong
    const highSpeed = makeMetric(30, { output_tokens: 10, duration_ms: 5000 });
    const lowSpeed = makeMetric(5, { output_tokens: 10, duration_ms: 5000 });
    const result = buildHistogramData([highSpeed, lowSpeed]);
    expect(result).not.toBeNull();
    expect(result!.min).toBeCloseTo(5);
    expect(result!.max).toBeCloseTo(30);
  });

  it("produces correct min and max from tokens_per_second values", () => {
    const metrics = [makeMetric(20), makeMetric(30), makeMetric(25)];
    const result = buildHistogramData(metrics);
    expect(result).not.toBeNull();
    expect(result!.min).toBeCloseTo(20);
    expect(result!.max).toBeCloseTo(30);
  });

  it("all values land in a single bin when min === max", () => {
    const metrics = [makeMetric(25), makeMetric(25), makeMetric(25)];
    const result = buildHistogramData(metrics);
    expect(result).not.toBeNull();
    const nonEmpty = result!.bins.filter((b) => b > 0);
    expect(nonEmpty).toHaveLength(1);
    expect(nonEmpty[0]).toBe(3);
  });

  it("all bin counts sum to the number of valid metrics", () => {
    const metrics = Array.from({ length: 50 }, (_, i) => makeMetric(10 + i * 0.5));
    const result = buildHistogramData(metrics);
    expect(result).not.toBeNull();
    const total = result!.bins.reduce((a, b) => a + b, 0);
    expect(total).toBe(50);
  });

  it("returns correct percentile values", () => {
    // 10 sorted values: 1..10
    const metrics = Array.from({ length: 10 }, (_, i) => makeMetric(i + 1));
    const result = buildHistogramData(metrics);
    expect(result).not.toBeNull();
    // floor(10 * 0.50) = 5 → sorted[5] = 6
    expect(result!.p50).toBe(6);
    // floor(10 * 0.95) = 9 → sorted[9] = 10
    expect(result!.p95).toBe(10);
    // floor(10 * 0.99) = 9 → sorted[9] = 10
    expect(result!.p99).toBe(10);
  });

  it("bin count is clamped between 10 and 30", () => {
    // 3 values → floor(3/5)=0 → clamped to 10
    const small = Array.from({ length: 3 }, (_, i) => makeMetric(i + 1));
    const smallResult = buildHistogramData(small);
    expect(smallResult!.bins).toHaveLength(10);

    // 200 values → floor(200/5)=40 → clamped to 30
    const large = Array.from({ length: 200 }, (_, i) => makeMetric(i + 1));
    const largeResult = buildHistogramData(large);
    expect(largeResult!.bins).toHaveLength(30);
  });
});
