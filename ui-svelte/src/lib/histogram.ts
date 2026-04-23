import type { Metrics } from "./types";

export interface HistogramData {
  bins: number[];
  min: number;
  max: number;
  binSize: number;
  p99: number;
  p95: number;
  p50: number;
}

export function buildHistogramData(metrics: Metrics[]): HistogramData | null {
  const valid = metrics.filter((m) => m.tokens_per_second >= 0);
  if (valid.length === 0) return null;

  const values = valid.map((m) => m.tokens_per_second);
  const sorted = [...values].sort((a, b) => a - b);
  const len = sorted.length;

  const p50 = sorted[Math.floor(len * 0.5)];
  const p95 = sorted[Math.floor(len * 0.95)];
  const p99 = sorted[Math.floor(len * 0.99)];

  const min = sorted[0];
  const max = sorted[len - 1];
  const binCount = Math.min(30, Math.max(10, Math.floor(len / 5)));
  const binSize = min === max ? 1 : (max - min) / binCount;

  const bins = Array(binCount).fill(0);
  values.forEach((v) => {
    const idx = Math.min(Math.floor((v - min) / binSize), binCount - 1);
    bins[idx]++;
  });

  return { bins, min, max, binSize, p50, p95, p99 };
}
