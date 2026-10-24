import { useCallback, useEffect, useMemo, useRef } from "react";
import { useLocation, useMatch, useNavigate, useSearch } from "@tanstack/react-router";

import { usePreference } from "@/lib/usePreference";

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
  // The list's own route, not the location: a list still mounted while the next page loads must not save under it.
  const pathname = useMatch({ strict: false, select: (match) => match.pathname });
  const showing = useLocation({ select: (location) => location.pathname }) === pathname;
  // Dots, not slashes: the key travels as one segment of /me/preferences/{key}.
  const [remembered, remember, loaded] = usePreference<Record<string, string>>(`list.view${pathname.replaceAll("/", ".")}`, {});
  const restored = useRef<string>(undefined);
  const restoring = useRef(false);

  // A list reached without a view of its own (the sidebar, back to a bare URL) gets the one it was left with, as the
  // *Arrs do; the title search is not kept.
  useEffect(() => {
    if (!showing) {
      // Kept mounted under another page: coming back to a bare URL is a return to restore from.
      restored.current = undefined;
      return;
    }
    if (!loaded || restoring.current) return;
    const shown = query.toRecord({ ...current, filters: Object.fromEntries(Object.entries(current.filters).filter(([key]) => key !== "q")) });
    if (restored.current !== pathname) {
      restored.current = pathname;
      const stored = query.toRecord(query.fromRecord(remembered, filterKeys));
      if (Object.keys(search).length === 0 && Object.keys(stored).length > 0) {
        restoring.current = true;
        void navigate({ to: ".", search: stored, replace: true }).finally(() => (restoring.current = false));
        return;
      }
    }
    if (!sameView(shown, remembered)) remember(shown);
  }, [loaded, showing, current, pathname, remembered, remember, filterKeys, search, navigate]);

  const setQuery = useCallback(
    (next: query.ListQuery) => {
      // replace, not push: changing a filter is not a navigation to go back from.
      void navigate({ to: ".", search: query.toRecord(next), replace: true });
    },
    [navigate],
  );

  return { query: current, setQuery };
}

function sameView(a: Record<string, string>, b: Record<string, string>): boolean {
  const entries = (view: Record<string, string>) => JSON.stringify(Object.entries(view).sort(([x], [y]) => x.localeCompare(y)));
  return entries(a) === entries(b);
}
