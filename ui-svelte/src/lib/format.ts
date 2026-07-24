// Shared display formatters used across the UI.

export interface FormatDurationOptions {
  /** Number of decimal places when rendering seconds. Default 2. */
  precision?: number;
  /** When true, render durations under 1s as whole milliseconds (e.g. "850ms"). */
  subSecondMs?: boolean;
}

/** Format a millisecond duration as a human-readable string. */
export function formatDuration(ms: number, opts: FormatDurationOptions = {}): string {
  const { precision = 2, subSecondMs = false } = opts;
  if (subSecondMs && ms < 1000) {
    return `${ms.toFixed(0)}ms`;
  }
  return `${(ms / 1000).toFixed(precision)}s`;
}

/** Format a tokens-per-second value; negative values are reported as "unknown". */
export function formatSpeed(speed: number): string {
  return speed < 0 ? "unknown" : speed.toFixed(2) + " t/s";
}

/** Format a byte count as B / KB / MB. */
export function formatFileSize(bytes: number): string {
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KB";
  return (bytes / (1024 * 1024)).toFixed(1) + " MB";
}

/** Format a timestamp as a relative time or local timestamp when older than a day. */
export function formatRelativeTime(timestamp: string): string {
  const now = new Date();
  const date = new Date(timestamp);
  const diffInSeconds = Math.floor((now.getTime() - date.getTime()) / 1000);
  if (diffInSeconds < 5) return "now";
  if (diffInSeconds < 60) return `${diffInSeconds}s ago`;
  const diffInMinutes = Math.floor(diffInSeconds / 60);
  if (diffInMinutes < 60) return `${diffInMinutes}m ago`;
  const diffInHours = Math.floor(diffInMinutes / 60);
  if (diffInHours < 24) return `${diffInHours}h ago`;
  const datePart = [
    date.getFullYear(),
    String(date.getMonth() + 1).padStart(2, "0"),
    String(date.getDate()).padStart(2, "0"),
  ].join("-");
  const timePart = [date.getHours(), date.getMinutes(), date.getSeconds()]
    .map((value) => String(value).padStart(2, "0"))
    .join(":");
  return `${datePart} ${timePart}`;
}
