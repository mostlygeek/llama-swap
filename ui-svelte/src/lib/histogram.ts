import type { HistogramData } from "./types";

export interface HistogramOptions {
  minBins?: number;
  maxBins?: number;
}

const DEFAULT_OPTIONS: HistogramOptions = {
  minBins: 5,
  maxBins: 20,
};

function percentile(sorted: number[], p: number): number {
  if (sorted.length === 0) return 0;
  if (sorted.length === 1) return sorted[0];

  const rank = (p / 100) * (sorted.length - 1);
  const lower = Math.floor(rank);
  const upper = Math.ceil(rank);
  const fraction = rank - lower;

  return sorted[lower] + fraction * (sorted[upper] - sorted[lower]);
}

export function calculateHistogramData(
  values: number[],
  options: HistogramOptions = DEFAULT_OPTIONS,
): HistogramData | null {
  if (values.length === 0) return null;

  const sorted = [...values].sort((a, b) => a - b);
  const min = sorted[0];
  const max = sorted[sorted.length - 1];

  const p50 = percentile(sorted, 50);
  const p95 = percentile(sorted, 95);
  const p99 = percentile(sorted, 99);

  if (min === max) {
    return {
      bins: [values.length],
      min,
      max,
      binSize: 0,
      p50,
      p95,
      p99,
    };
  }

  const { minBins = 5, maxBins = 20 } = options;
  const sturges = Math.ceil(Math.log2(values.length)) + 1;
  const binCount = Math.min(maxBins, Math.max(minBins, sturges));
  const binSize = (max - min) / binCount;

  const bins = new Array(binCount).fill(0);
  for (const value of values) {
    const binIndex = Math.min(Math.floor((value - min) / binSize), binCount - 1);
    bins[binIndex]++;
  }

  return {
    bins,
    min,
    max,
    binSize,
    p50,
    p95,
    p99,
  };
}
