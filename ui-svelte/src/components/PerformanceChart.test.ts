import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

// Mounting PerformanceChart.svelte would require a DOM/canvas testing
// environment that this project doesn't have configured (no jsdom/happy-dom,
// no @testing-library/svelte). These tests instead assert on the component
// source to guard the tree-shaking change: only the specific Chart.js
// building blocks this component needs should be registered, instead of
// Chart.js's full `registerables` bundle (every chart type/scale/plugin).
const source = readFileSync(
  fileURLToPath(new URL("./PerformanceChart.svelte", import.meta.url)),
  "utf-8",
);

const EXPECTED_CHART_PIECES = [
  "LineController",
  "LineElement",
  "PointElement",
  "LinearScale",
  "CategoryScale",
  "Legend",
  "Title",
  "Tooltip",
];

function extractList(pattern: RegExp): string[] {
  const match = source.match(pattern);
  expect(match).not.toBeNull();
  return match![1]
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

describe("PerformanceChart.svelte Chart.js registration", () => {
  it("does not import or register the full 'registerables' bundle", () => {
    expect(source).not.toMatch(/registerables/);
  });

  it("imports exactly Chart plus the line-chart building blocks it needs", () => {
    const imported = extractList(/import\s*\{([^}]+)\}\s*from\s*"chart\.js"/);
    expect(new Set(imported)).toEqual(new Set(["Chart", ...EXPECTED_CHART_PIECES]));
  });

  it("registers exactly the imported building blocks via Chart.register", () => {
    const registered = extractList(/Chart\.register\(([^)]+)\)/);
    expect(registered).toEqual(EXPECTED_CHART_PIECES);
  });

  it("registers each imported piece exactly once (no duplicates)", () => {
    const registered = extractList(/Chart\.register\(([^)]+)\)/);
    expect(new Set(registered).size).toBe(registered.length);
  });

  it("does not register unrelated chart types this component doesn't use (e.g. BarController, PieController)", () => {
    const registered = extractList(/Chart\.register\(([^)]+)\)/);
    expect(registered).not.toContain("BarController");
    expect(registered).not.toContain("PieController");
    expect(registered).not.toContain("DoughnutController");
  });
});