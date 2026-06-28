import type { HistogramData, StackedHistogramData } from "./types";

export const stackedHistogramColors = [
  "#3b82f6", // blue
  "#f59e0b", // amber
  "#10b981", // emerald
  "#ef4444", // red
  "#8b5cf6", // violet
  "#06b6d4", // cyan
  "#ec4899", // pink
  "#f97316", // orange
  "#14b8a6", // teal
  "#6366f1", // indigo
  "#84cc16", // lime
  "#e11d48", // rose
];

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

export interface StackedMetric {
  model: string;
  value: number;
}

export function calculateStackedHistogramData(
  entries: StackedMetric[],
  options: HistogramOptions = DEFAULT_OPTIONS,
): StackedHistogramData | null {
  const values = entries.map((e) => e.value);
  if (values.length === 0) return null;

  const sorted = [...values].sort((a, b) => a - b);
  const min = sorted[0];
  const max = sorted[sorted.length - 1];

  const p50 = percentile(sorted, 50);
  const p95 = percentile(sorted, 95);
  const p99 = percentile(sorted, 99);

  // Group by model, preserving insertion order
  const modelOrder = new Map<string, number>();
  const modelValues = new Map<string, number[]>();
  for (const entry of entries) {
    if (!modelOrder.has(entry.model)) {
      modelOrder.set(entry.model, modelOrder.size);
      modelValues.set(entry.model, []);
    }
    modelValues.get(entry.model)!.push(entry.value);
  }

  // Sort models alphabetically for deterministic stacking order
  const sortedModels = [...modelOrder.keys()].sort();
  const modelIndexMap = new Map<string, number>();
  sortedModels.forEach((m, i) => modelIndexMap.set(m, i));

  if (min === max) {
    const segments: { model: string; count: number }[] = sortedModels.map((model) => ({
      model,
      count: (modelValues.get(model) ?? []).length,
    }));
    return {
      bins: [
        {
          binStart: min,
          binEnd: max,
          segments,
          totalCount: values.length,
        },
      ],
      min,
      max,
      binSize: 0,
      p50,
      p95,
      p99,
      models: sortedModels,
    };
  }

  const { minBins = 5, maxBins = 20 } = options;
  const sturges = Math.ceil(Math.log2(values.length)) + 1;
  const binCount = Math.min(maxBins, Math.max(minBins, sturges));
  const binSize = (max - min) / binCount;

  const bins: { binStart: number; binEnd: number; segments: { model: string; count: number }[]; totalCount: number }[] =
    [];
  for (let i = 0; i < binCount; i++) {
    const binStart = min + i * binSize;
    const binEnd = binStart + binSize;
    const segments: { model: string; count: number }[] = [];
    let totalCount = 0;

    for (const model of sortedModels) {
      const mValues = modelValues.get(model) ?? [];
      let count = 0;
      for (const v of mValues) {
        const idx = Math.min(Math.floor((v - min) / binSize), binCount - 1);
        if (idx === i) {
          count++;
        }
      }
      if (count > 0) {
        segments.push({ model, count });
        totalCount += count;
      }
    }

    bins.push({ binStart, binEnd, segments, totalCount });
  }

  return {
    bins,
    min,
    max,
    binSize,
    p50,
    p95,
    p99,
    models: sortedModels,
  };
}
