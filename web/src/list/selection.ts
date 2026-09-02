// Selection is a query, not a set of rows (section 21.7).
// See documentation/web-interface.md.

export type Id = number;

export type Selection =
  | { readonly mode: "none" }
  | { readonly mode: "include"; readonly ids: ReadonlySet<Id> }
  | { readonly mode: "all"; readonly excluded: ReadonlySet<Id> };

export const NOTHING: Selection = { mode: "none" };

/** Every item matching the current filters, including unfetched ones. */
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
      // "Nothing", not "an empty selection": callers ask which.
      return ids.size === 0 ? NOTHING : { mode: "include", ids };
    }
    case "all": {
      const excluded = new Set(selection.excluded);
      if (!excluded.delete(id)) excluded.add(id);
      return { mode: "all", excluded };
    }
  }
}

/** Shift-click. Only spans loaded ids: the client knows no other order. */
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

/** Null when unknowable: "all" needs a server total, and the loaded-row count
 *  would show "12 selected" to someone who asked for all 231. */
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

/** The wire form: section 21.7's `{filters, excludedIDs}`, not a list of ids. */
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
