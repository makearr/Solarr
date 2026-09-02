// The list's view state, kept apart from the hook so it is testable without React.

export type SortDirection = "asc" | "desc";

export interface ListQuery {
  readonly filters: Readonly<Record<string, string>>;
  readonly sort: string | null;
  readonly direction: SortDirection;
}

export const EMPTY: ListQuery = { filters: {}, sort: null, direction: "asc" };

/** The URL wins over stored preferences (section 21.2). Unknown parameters are
 *  ignored: a URL is user input. */
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

/** Defaults are omitted, so identical views produce identical links. */
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

/** Cache key. Entries are sorted, or the same view fetches twice and drifts. */
export function key(query: ListQuery): string {
  const filters = Object.entries(query.filters).sort(([a], [b]) => a.localeCompare(b));
  return JSON.stringify({ filters, sort: query.sort, direction: query.direction });
}
