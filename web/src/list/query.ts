// The list's view state, kept apart from the hook so it is testable without React.

export type SortDirection = "asc" | "desc";

export interface ListQuery {
  readonly filters: Readonly<Record<string, string>>;
  readonly sort: string | null;
  readonly direction: SortDirection;
}

export const EMPTY: ListQuery = { filters: {}, sort: null, direction: "asc" };

/** The URL wins over stored preferences (section 21.2). Unknown parameters are
 *  ignored, and so is anything that is not a string: a URL is user input. */
export function fromRecord(
  record: Record<string, unknown>,
  filterKeys: readonly string[],
): ListQuery {
  const filters: Record<string, string> = {};
  for (const key of filterKeys) {
    const value = record[key];
    if (typeof value === "string" && value !== "") filters[key] = value;
  }

  return {
    filters,
    sort: typeof record.sort === "string" && record.sort !== "" ? record.sort : null,
    direction: record.dir === "desc" ? "desc" : "asc",
  };
}

/** Defaults are omitted, so identical views produce identical links. */
export function toRecord(query: ListQuery): Record<string, string> {
  const record: Record<string, string> = {};
  for (const [key, value] of Object.entries(query.filters)) {
    if (value !== "") record[key] = value;
  }
  if (query.sort) {
    record.sort = query.sort;
    if (query.direction === "desc") record.dir = "desc";
  }
  return record;
}

/** The router hands over a parsed object; these two are for the raw string a
 *  test or a deep link starts from. */
export function fromSearch(search: string, filterKeys: readonly string[]): ListQuery {
  return fromRecord(Object.fromEntries(new URLSearchParams(search)), filterKeys);
}

export function toSearch(query: ListQuery): string {
  const encoded = new URLSearchParams(toRecord(query)).toString();
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
