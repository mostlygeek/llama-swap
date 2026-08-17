import { describe, expect, it } from "vitest";
import {
  activeFilterCount,
  appendActivityFilters,
  emptyActivityFilters,
  hasActiveFilters,
  normalizeActivityFilters,
  type ActivityFilters,
} from "./activityFilters";

function build(overrides: Partial<ActivityFilters> = {}): ActivityFilters {
  return { ...emptyActivityFilters(), ...overrides };
}

function paramsFor(filters: ActivityFilters): URLSearchParams {
  const query = new URLSearchParams();
  appendActivityFilters(query, filters);
  return query;
}

describe("emptyActivityFilters", () => {
  it("returns a fresh object each call", () => {
    const first = emptyActivityFilters();
    first.minID = "5";
    expect(emptyActivityFilters().minID).toBe("");
  });
});

describe("appendActivityFilters", () => {
  it("adds nothing for empty filters", () => {
    expect(paramsFor(emptyActivityFilters()).toString()).toBe("");
  });

  it("sets id bounds", () => {
    const query = paramsFor(build({ minID: "5", maxID: "10" }));
    expect(query.get("min_id")).toBe("5");
    expect(query.get("max_id")).toBe("10");
  });

  it("drops blank, non-numeric and out-of-range ids", () => {
    for (const value of ["", "   ", "abc", "0", "-3", "1.5"]) {
      expect(paramsFor(build({ minID: value })).has("min_id")).toBe(false);
    }
  });

  it("never writes a model param", () => {
    expect(paramsFor(build({ minID: "2" })).has("model")).toBe(false);
  });

  it("combines every filter dimension", () => {
    expect(paramsFor(build({ minID: "2", maxID: "9" })).toString()).toBe("min_id=2&max_id=9");
  });
});

describe("activeFilterCount", () => {
  it("is zero for empty filters", () => {
    expect(activeFilterCount(emptyActivityFilters())).toBe(0);
    expect(hasActiveFilters(emptyActivityFilters())).toBe(false);
  });

  it("counts each set field", () => {
    expect(activeFilterCount(build({ minID: "1" }))).toBe(1);
    const filters = build({ minID: "1", maxID: "9" });
    expect(activeFilterCount(filters)).toBe(2);
    expect(hasActiveFilters(filters)).toBe(true);
  });
});

describe("normalizeActivityFilters", () => {
  it("falls back to empty for non-objects", () => {
    for (const value of [null, undefined, 42, "nope", true]) {
      expect(normalizeActivityFilters(value)).toEqual(emptyActivityFilters());
    }
  });

  it("keeps valid fields and drops wrongly typed ones", () => {
    expect(normalizeActivityFilters({ minID: "3", maxID: 5 })).toEqual(build({ minID: "3" }));
  });

  it("ignores unknown keys", () => {
    expect(normalizeActivityFilters({ minID: "3", userAgent: "x" })).toEqual(build({ minID: "3" }));
  });

  // Filters persisted by an earlier build that had a date range and a model
  // multi-select must not carry those fields back into the current shape.
  it("drops start, end and models left over in stored filters", () => {
    const restored = normalizeActivityFilters({
      start: "2026-08-01T00:00",
      end: "2026-08-16T00:00",
      models: ["m1", "m2"],
      minID: "2",
    });
    expect(restored).toEqual(build({ minID: "2" }));
    expect(paramsFor(restored).toString()).toBe("min_id=2");
  });
});
