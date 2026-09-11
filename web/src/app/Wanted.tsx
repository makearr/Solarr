import { useMemo } from "react";

import { useParams } from "@tanstack/react-router";

import { api } from "@/api/client";
import { ListTable, type Column } from "@/list/ListTable";
import { useLibraryList } from "@/list/useLibraryList";
import { useRouteView, type ViewState } from "@/list/useRouteView";
import type { components } from "@/api/schema";

type Work = components["schemas"]["Work"];

const ROW_HEIGHT = 36;

const STATE_LABEL: Record<string, string> = {
  MISSING: "Missing",
  PARTIAL: "Partial",
  UPGRADABLE: "Upgradable",
  COMPLETE: "Complete",
};

function StateBadge({ state }: { state?: string }) {
  if (!state) return null;
  return (
    <span
      className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${
        state === "MISSING" ? "border-danger text-danger" : "border-warn text-warn"
      }`}
    >
      {STATE_LABEL[state] ?? state}
    </span>
  );
}

/** What is monitored for this library but not yet fully present -- reuses
 *  /works' own list (its new wanted=true filter, 2026-09-11) rather than a
 *  second endpoint: a desired_item is a fact about a work, and the library
 *  list already carries every visibility/pagination rule that matters here.
 *  Acquisition does not exist yet (stage 5), so this is a read-only queue
 *  for now -- monitor toggling still lives on the work/artist screens. */
export function Wanted() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  // category_id is injected into the query's own filters (not read from the
  // URL) so it is part of this list's cache key -- Library.tsx's own
  // pattern, needed for the same reason: switching libraries must not show
  // a stale page cached under the previous one's key.
  const baseView = useRouteView([]);
  const routeView = useMemo<ViewState>(
    () => ({
      query: { ...baseView.query, filters: { ...baseView.query.filters, category_id: categoryId ?? "" } },
      setQuery: baseView.setQuery,
    }),
    [baseView, categoryId],
  );

  const list = useLibraryList<Work>(
    {
      name: "wanted",
      rowHeight: ROW_HEIGHT,
      idOf: (work) => work.id,
      fetchPage: async ({ cursor, signal }) => {
        const { data, error } = await api.GET("/works", {
          params: {
            query: {
              cursor,
              limit: 100,
              category_id: categoryId ? Number(categoryId) : undefined,
              wanted: true,
            },
          },
          signal,
        });
        if (error) throw error;
        return data;
      },
    },
    routeView,
  );

  const columns = useMemo<Column<Work>[]>(
    () => [
      { key: "title", header: "Title", render: (work) => work.title },
      { key: "artist", header: "Artist", render: (work) => work.artist ?? "" },
      { key: "year", header: "Released", width: "8rem", render: (work) => work.year ?? "" },
      {
        key: "wanted_state",
        header: "",
        width: "8rem",
        render: (work) => <StateBadge state={work.wanted_state} />,
      },
    ],
    [],
  );

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-center gap-3 border-b border-border-subtle px-4 py-2">
        <h2 className="text-sm font-medium">Wanted</h2>
        <span className="text-xs text-ink-muted">
          Monitored and not yet complete. Acquisition is not built yet (stage 5) -- this is what
          it will act on.
        </span>
      </div>

      <div className="min-h-0 flex-1">
        {list.isLoading && <Message>Loading…</Message>}
        {list.error !== null && <Message>Wanted items could not be read.</Message>}
        {!list.isLoading && list.error === null && list.items.length === 0 && (
          <Message>Nothing wanted -- every monitored item in this library is complete.</Message>
        )}
        {list.items.length > 0 && <ListTable list={list} columns={columns} idOf={(work) => work.id} />}
      </div>
    </div>
  );
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
