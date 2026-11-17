import type { HistogramData } from "./types";

export type PercentileKey = "p50" | "p95" | "p99";

/**
 * Percentile markers that fall inside the binned range, in drawing order.
 *
 * Percentiles are computed from every value, including the outliers the
 * backend leaves out of the bins, so a marker can sit above max or below min.
 * Drawing it would put the line outside the chart, so such markers are
 * dropped. With a single bucket (min == max) there is no x range to place a
 * marker in, so none is drawn either.
 */
export function visiblePercentiles(data: HistogramData): { key: PercentileKey; value: number }[] {
  if (data.max === data.min) {
    return [];
  }
  const markers: { key: PercentileKey; value: number }[] = [
    { key: "p50", value: data.p50 },
    { key: "p95", value: data.p95 },
    { key: "p99", value: data.p99 },
  ];
  return markers.filter(({ value }) => value >= data.min && value <= data.max);
}

/** Note telling how many values the histogram leaves out; null when none. */
export function formatOutlierNote(count: number): string | null {
  if (count <= 0) {
    return null;
  }
  return count === 1 ? "1 outlier hidden" : `${count} outliers hidden`;
}
