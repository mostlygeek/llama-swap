// Filter state for the activity table's filter drawer, kept separate from the
// component so the query-param building is unit testable.

export interface ActivityFilters {
  // Kept as strings so a cleared number input stays empty rather than 0.
  minID: string;
  maxID: string;
}

export function emptyActivityFilters(): ActivityFilters {
  return { minID: "", maxID: "" };
}

/**
 * normalizeActivityFilters coerces an unknown value (typically JSON restored
 * from localStorage, possibly written by an older build) into a valid
 * ActivityFilters. Unrecognized or wrongly typed fields fall back to empty.
 */
export function normalizeActivityFilters(raw: unknown): ActivityFilters {
  const filters = emptyActivityFilters();
  if (typeof raw !== "object" || raw === null) return filters;

  const source = raw as Record<string, unknown>;
  for (const key of ["minID", "maxID"] as const) {
    if (typeof source[key] === "string") filters[key] = source[key] as string;
  }
  return filters;
}

/** activeFilterCount counts how many filter fields are set. */
export function activeFilterCount(filters: ActivityFilters): number {
  let count = 0;
  if (filters.minID !== "") count++;
  if (filters.maxID !== "") count++;
  return count;
}

export function hasActiveFilters(filters: ActivityFilters): boolean {
  return activeFilterCount(filters) > 0;
}

// toPositiveInt keeps out blank, non-numeric and out-of-range ids so the API
// never has to reject input the UI could have caught.
function toPositiveInt(value: string): number | null {
  if (value.trim() === "") return null;
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed < 1) return null;
  return parsed;
}

/**
 * appendActivityFilters writes the set filters onto a URLSearchParams as the
 * params /api/metrics/activity accepts. It never writes "model": the model a
 * caller pins is set separately by getActivity.
 */
export function appendActivityFilters(query: URLSearchParams, filters: ActivityFilters): void {
  const minID = toPositiveInt(filters.minID);
  if (minID !== null) query.set("min_id", String(minID));

  const maxID = toPositiveInt(filters.maxID);
  if (maxID !== null) query.set("max_id", String(maxID));
}
