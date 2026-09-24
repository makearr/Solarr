import { useMemo, useState } from "react";

import { Link, useParams } from "@tanstack/react-router";
import { useMutation } from "@tanstack/react-query";
import { Search } from "lucide-react";

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

/** What is monitored for this library but not yet fully present -- /works' own wanted=true list.
 *  "Search all missing" runs the library's background search; each row searches its own album
 *  (Lidarr's Wanted -> Missing, asked for 2026-09-19). */
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

  const [rowResult, setRowResult] = useState<Record<number, string>>({});
  const searchRow = useMutation({
    mutationFn: async (workId: number) => {
      const { data, error } = await api.POST("/works/{id}/search", { params: { path: { id: workId } } });
      if (error) throw error;
      return data;
    },
    onMutate: (workId) => setRowResult((held) => ({ ...held, [workId]: "Searching…" })),
    onSuccess: (data, workId) => {
      const accepted = data.decisions.filter((decision) => decision.outcome === "ACCEPT").length;
      const found = data.decisions.length;
      setRowResult((held) => ({
        ...held,
        [workId]:
          found > 0
            ? `${found} found, ${accepted} acceptable`
            : data.indexer_errors.length > 0
              ? `${new Set(data.indexer_errors.map((failure) => failure.indexer_id)).size} indexer(s) could not be searched`
              : "Nothing found",
      }));
    },
    onError: (_, workId) => setRowResult((held) => ({ ...held, [workId]: "Search failed -- is an indexer connected?" })),
  });

  const searchAll = useMutation({
    mutationFn: async () => {
      if (!categoryId) return undefined;
      const { data, error } = await api.POST("/categories/{id}/search", { params: { path: { id: Number(categoryId) } } });
      if (error) throw error;
      return data;
    },
  });

  const columns = useMemo<Column<Work>[]>(
    () => [
      {
        key: "title",
        header: "Title",
        render: (work) => (
          <Link
            to="/works/$id"
            params={{ id: String(work.id) }}
            onClick={(event) => event.stopPropagation()}
            className="hover:underline"
          >
            {work.title}
          </Link>
        ),
      },
      { key: "artist", header: "Artist", render: (work) => work.artist ?? "" },
      { key: "year", header: "Released", width: "8rem", render: (work) => work.year ?? "" },
      {
        key: "wanted_state",
        header: "",
        width: "8rem",
        render: (work) => <StateBadge state={work.wanted_state} />,
      },
      {
        key: "search",
        header: "",
        width: "16rem",
        render: (work) => (
          <span className="flex items-center justify-end gap-2 text-xs text-ink-muted">
            {rowResult[work.id]}
            <button
              type="button"
              aria-label={`Search for ${work.title}`}
              disabled={searchRow.isPending && searchRow.variables === work.id}
              onClick={(event) => {
                // The row itself selects on click; searching one album must not.
                event.stopPropagation();
                searchRow.mutate(work.id);
              }}
              className="rounded-md border border-border-subtle p-1 hover:text-ink disabled:opacity-50"
            >
              <Search aria-hidden className="size-3.5" />
            </button>
          </span>
        ),
      },
    ],
    [rowResult, searchRow],
  );

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-center gap-3 border-b border-border-subtle px-4 py-2">
        <h2 className="text-sm font-medium">Wanted</h2>
        <span className="text-xs text-ink-muted">Monitored and not yet complete.</span>
        <span className="ml-auto flex items-center gap-2 text-xs text-ink-muted">
          {searchAll.isSuccess &&
            (searchAll.data?.duplicate ? "A search of this library is already running." : "Searching in the background.")}
          {searchAll.isError && <span className="text-danger">The search could not start.</span>}
          <button
            type="button"
            disabled={searchAll.isPending || list.items.length === 0}
            onClick={() => searchAll.mutate()}
            className="flex items-center gap-1 rounded-md border border-border-subtle px-2 py-1 hover:text-ink disabled:opacity-50"
          >
            <Search aria-hidden className="size-3.5" />
            Search all missing
          </button>
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
