import { describe, it, expect, vi, afterEach } from "vitest";
import { formatDuration, formatSpeed, formatFileSize, formatRelativeTime } from "./format";

describe("formatDuration", () => {
  it("defaults to seconds with 2 decimals", () => {
    expect(formatDuration(1500)).toBe("1.50s");
    expect(formatDuration(0)).toBe("0.00s");
    expect(formatDuration(250)).toBe("0.25s");
  });

  it("honors custom precision", () => {
    expect(formatDuration(1500, { precision: 1 })).toBe("1.5s");
  });

  it("renders sub-second durations as ms when subSecondMs is set", () => {
    expect(formatDuration(850, { precision: 1, subSecondMs: true })).toBe("850ms");
    expect(formatDuration(1500, { precision: 1, subSecondMs: true })).toBe("1.5s");
    expect(formatDuration(999, { subSecondMs: true })).toBe("999ms");
  });
});

describe("formatSpeed", () => {
  it("formats tokens per second", () => {
    expect(formatSpeed(42.5)).toBe("42.50 t/s");
    expect(formatSpeed(0)).toBe("0.00 t/s");
  });

  it("reports negative values as unknown", () => {
    expect(formatSpeed(-1)).toBe("unknown");
  });
});

describe("formatFileSize", () => {
  it("formats bytes", () => {
    expect(formatFileSize(512)).toBe("512 B");
  });

  it("formats kilobytes", () => {
    expect(formatFileSize(2048)).toBe("2.0 KB");
  });

  it("formats megabytes", () => {
    expect(formatFileSize(5 * 1024 * 1024)).toBe("5.0 MB");
  });
});

describe("formatRelativeTime", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("formats relative times", () => {
    const now = new Date("2026-06-28T12:00:00Z");
    vi.useFakeTimers();
    vi.setSystemTime(now);

    expect(formatRelativeTime("2026-06-28T11:59:58Z")).toBe("now");
    expect(formatRelativeTime("2026-06-28T11:59:30Z")).toBe("30s ago");
    expect(formatRelativeTime("2026-06-28T11:55:00Z")).toBe("5m ago");
    expect(formatRelativeTime("2026-06-28T09:00:00Z")).toBe("3h ago");
    const olderThanOneDay = new Date(2026, 5, 25, 12, 34, 56);
    expect(formatRelativeTime(olderThanOneDay.toISOString())).toBe("2026-06-25 12:34:56");
  });
});
