// The list's view state: what is being asked for, not what came back.
//
// Kept apart from the hook so it can be tested without React, and so the URL
// round-trip is one place rather than scattered through effects.

export type SortDirection = "asc" | "desc";

export interface ListQuery {
  readonly filters: Readonly<Record<string, string>>;
  readonly sort: string | null;
  readonly direction: SortDirection;
}

export const EMPTY: ListQuery = { filters: {}, sort: null, direction: "asc" };

/**
 * Reads view state out of a URL.
 *
 * The URL wins over stored preferences when it carries state, so a link to a
 * particular view opens as that view (section 21.2). Unknown parameters are
 * ignored rather than rejected: a URL is user input and may carry anything.
 */
export function fromSearch(search: string, filterKeys: readonly string[]): ListQuery {
  const parameters = new URLSearchParams(search);

  const filters: Record<string, string> = {};
  for (const key of filterKeys) {
    const value = parameters.get(key);
    if (value !== null && value !== "") filters[key] = value;
  }

  const direction = parameters.get("dir");
  return {
    filters,
    sort: parameters.get("sort") || null,
    direction: direction === "desc" ? "desc" : "asc",
  };
}

/**
 * Writes view state into a query string.
 *
 * Defaults are omitted rather than spelled out, so an untouched view has a
 * clean URL and two identical views produce the same link.
 */
export function toSearch(query: ListQuery): string {
  const parameters = new URLSearchParams();
  for (const [key, value] of Object.entries(query.filters)) {
    if (value !== "") parameters.set(key, value);
  }
  if (query.sort) {
    parameters.set("sort", query.sort);
    if (query.direction === "desc") parameters.set("dir", "desc");
  }

  const encoded = parameters.toString();
  return encoded === "" ? "" : `?${encoded}`;
}

/** Cycles a column: ascending, descending, then unsorted. */
export function cycleSort(query: ListQuery, column: string): ListQuery {
  if (query.sort !== column) return { ...query, sort: column, direction: "asc" };
  if (query.direction === "asc") return { ...query, sort: column, direction: "desc" };
  return { ...query, sort: null, direction: "asc" };
}

export function withFilter(query: ListQuery, key: string, value: string): ListQuery {
  const filters = { ...query.filters };
  if (value === "") delete filters[key];
  else filters[key] = value;
  return { ...query, filters };
}

/**
 * The identity of a query, for a cache key.
 *
 * Object key order is not stable across the ways a filter can be built, so the
 * entries are sorted -- otherwise the same view fetches twice and the two
 * copies drift.
 */
export function key(query: ListQuery): string {
  const filters = Object.entries(query.filters).sort(([a], [b]) => a.localeCompare(b));
  return JSON.stringify({ filters, sort: query.sort, direction: query.direction });
}
