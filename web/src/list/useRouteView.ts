import { useCallback, useMemo } from "react";
import { useNavigate, useSearch } from "@tanstack/react-router";

import * as query from "./query";

/** Where a list's view state lives. The router owns it, so a figure on the
 *  dashboard can link into a filtered view and the link works (section 21.12). */
export interface ViewState {
  readonly query: query.ListQuery;
  readonly setQuery: (next: query.ListQuery) => void;
}

/** Binds a list's view state to the URL through the router. Reading
 *  window.location instead leaves two owners of one URL: the router's
 *  navigations become invisible to the list, and the list's edits to the router. */
export function useRouteView(filterKeys: readonly string[]): ViewState {
  const search = useSearch({ strict: false }) as Record<string, unknown>;
  const navigate = useNavigate();

  const current = useMemo(() => query.fromRecord(search, filterKeys), [search, filterKeys]);

  const setQuery = useCallback(
    (next: query.ListQuery) => {
      // replace, not push: changing a filter is not a navigation to go back from.
      void navigate({ to: ".", search: query.toRecord(next), replace: true });
    },
    [navigate],
  );

  return { query: current, setQuery };
}
