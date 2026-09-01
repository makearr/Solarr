// Selection is a query, not a set of rows (section 21.7).
//
// "Select all matching" has to cover items the virtualised list has never
// fetched, and deselecting three of 231 must not require materialising the
// other 228. So the selection is carried as a mode plus a small exception set,
// and the exception set is the *only* thing that grows with clicking.

export type Id = number;

export type Selection =
  | { readonly mode: "none" }
  | { readonly mode: "include"; readonly ids: ReadonlySet<Id> }
  | { readonly mode: "all"; readonly excluded: ReadonlySet<Id> };

export const NOTHING: Selection = { mode: "none" };

/** ALL selects every item matching the current filters, including unfetched ones. */
export const ALL: Selection = { mode: "all", excluded: new Set() };

export function isSelected(selection: Selection, id: Id): boolean {
  switch (selection.mode) {
    case "none":
      return false;
    case "include":
      return selection.ids.has(id);
    case "all":
      return !selection.excluded.has(id);
  }
}

export function toggle(selection: Selection, id: Id): Selection {
  switch (selection.mode) {
    case "none":
      return { mode: "include", ids: new Set([id]) };
    case "include": {
      const ids = new Set(selection.ids);
      if (!ids.delete(id)) ids.add(id);
      // An empty include set is "nothing", not "an empty selection": the
      // difference matters to every caller that asks whether anything is
      // selected at all.
      return ids.size === 0 ? NOTHING : { mode: "include", ids };
    }
    case "all": {
      const excluded = new Set(selection.excluded);
      if (!excluded.delete(id)) excluded.add(id);
      return { mode: "all", excluded };
    }
  }
}

/**
 * Extends the selection across a range, for shift-click.
 *
 * The range is taken from the ids currently loaded, which is the only order the
 * client knows. Selecting a range it has not fetched is not expressible and
 * must not be faked -- that is what "select all matching" is for.
 */
export function selectRange(selection: Selection, loaded: readonly Id[], from: Id, to: Id): Selection {
  const start = loaded.indexOf(from);
  const end = loaded.indexOf(to);
  if (start < 0 || end < 0) return selection;

  const [low, high] = start <= end ? [start, end] : [end, start];
  const span = loaded.slice(low, high + 1);

  if (selection.mode === "all") {
    const excluded = new Set(selection.excluded);
    for (const id of span) excluded.delete(id);
    return { mode: "all", excluded };
  }

  const ids = new Set(selection.mode === "include" ? selection.ids : []);
  for (const id of span) ids.add(id);
  return { mode: "include", ids };
}

/**
 * Counts the selection, given how many items match the filters in total.
 *
 * Returns null when the count is not knowable: "all" needs a server-side total,
 * and until one arrives the honest answer is "unknown" rather than the number
 * of rows that happen to be loaded. Showing "12 selected" when the user asked
 * for all 231 is worse than showing nothing.
 */
export function count(selection: Selection, matching: number | null): number | null {
  switch (selection.mode) {
    case "none":
      return 0;
    case "include":
      return selection.ids.size;
    case "all":
      return matching === null ? null : Math.max(0, matching - selection.excluded.size);
  }
}

export function isEmpty(selection: Selection): boolean {
  return selection.mode === "none" || (selection.mode === "include" && selection.ids.size === 0);
}

/**
 * The wire form: what a bulk action sends to the server.
 *
 * Deliberately mirrors section 21.7's `{filters, excludedIDs}` rather than a
 * list of ids, so the server re-runs the query and acts on what matches now.
 */
export type SelectionRequest =
  | { readonly kind: "ids"; readonly ids: Id[] }
  | { readonly kind: "query"; readonly filters: Readonly<Record<string, string>>; readonly excluded: Id[] };

export function toRequest(
  selection: Selection,
  filters: Readonly<Record<string, string>>,
): SelectionRequest | null {
  switch (selection.mode) {
    case "none":
      return null;
    case "include":
      return selection.ids.size === 0 ? null : { kind: "ids", ids: [...selection.ids].sort((a, b) => a - b) };
    case "all":
      return { kind: "query", filters, excluded: [...selection.excluded].sort((a, b) => a - b) };
  }
}
