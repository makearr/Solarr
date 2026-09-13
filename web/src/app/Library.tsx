import { useMemo, useRef, useState } from "react";

import { Link, useParams } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";

import { api } from "@/api/client";
import { PosterGrid } from "@/list/PosterGrid";
import { ListTable, type Column } from "@/list/ListTable";
import { useLibraryList } from "@/list/useLibraryList";
import { useRouteView, type ViewState } from "@/list/useRouteView";
import { useGridMetrics } from "@/list/useGridMetrics";
import { useNarrow } from "@/list/useNarrow";
import { useRecordLastLevel } from "@/list/useRecordLastLevel";
import type { SelectionRequest } from "@/list/selection";
import type { components } from "@/api/schema";
import { CoverArt } from "./CoverArt";
import { Poster } from "./Poster";

type Work = components["schemas"]["Work"];

// The request body isn't a named schema component (it's inline in
// openapi.yaml), so openapi-typescript has nothing to hang a name on --
// spelled out directly rather than reached for through api.POST's own types.
interface BulkMonitorBody {
  ids?: number[];
  category_id?: number;
  excluded_ids?: number[];
  monitored: boolean;
  preview?: boolean;
}

// Selection is a query, never a fetched list of ids (section 21.7) -- the
// request carries either explicit ids or the current filter plus what the
// user deselected from "select all matching".
function bulkBody(request: SelectionRequest, monitored: boolean, preview: boolean): BulkMonitorBody {
  if (request.kind === "ids") {
    return { ids: request.ids, monitored, preview };
  }
  const categoryId = request.filters.category_id;
  return {
    category_id: categoryId ? Number(categoryId) : undefined,
    excluded_ids: request.excluded,
    monitored,
    preview,
  };
}
type WorkSort = "title" | "year" | "added";

// Title plus a year/media-type line, padding included.
const CARD_HEIGHT = 68;
// A 32px thumbnail plus padding, for a table row.
const TABLE_ROW_HEIGHT = 44;
// The poster wall's own geometry, matching LibraryLevelList's.
const POSTER_MIN_WIDTH = 168;
const POSTER_GAP = 16;
const POSTER_PADDING = 16;
const POSTER_LABEL_HEIGHT = 46;
const VIEW_STORAGE_KEY = "medianexus.library.view";

/** Albums open as a poster wall (Lidarr's own album view, 2026-09-13); the
 *  table is a click away and remembered once chosen. */
function loadView(): "table" | "poster" {
  try {
    return localStorage.getItem(VIEW_STORAGE_KEY) === "table" ? "table" : "poster";
  } catch {
    return "poster";
  }
}

// Cover and title are always shown -- they are how a row is identified at
// all. The rest is what section 21's "configurable columns" picks among.
const OPTIONAL_COLUMNS = ["artist", "year", "added_at"] as const;
type OptionalColumn = (typeof OPTIONAL_COLUMNS)[number];
const COLUMN_LABELS: Record<OptionalColumn, string> = {
  artist: "Artist",
  year: "Released",
  added_at: "Added",
};
const COLUMNS_STORAGE_KEY = "medianexus.library.columns";

function loadVisibleColumns(): OptionalColumn[] {
  try {
    const raw = localStorage.getItem(COLUMNS_STORAGE_KEY);
    if (!raw) return [...OPTIONAL_COLUMNS];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [...OPTIONAL_COLUMNS];
    return OPTIONAL_COLUMNS.filter((column) => parsed.includes(column));
  } catch {
    return [...OPTIONAL_COLUMNS];
  }
}

function saveVisibleColumns(columns: readonly OptionalColumn[]) {
  try {
    localStorage.setItem(COLUMNS_STORAGE_KEY, JSON.stringify(columns));
  } catch {
    // A per-browser convenience; losing it costs nothing worth failing over.
  }
}

// Maps a column key to the sort value the /works API understands. Artist has
// no entry: it comes from a join with no supporting index (WorkRow.Artist's
// own doc comment), so it is shown but not sortable.
const SORT_KEYS: Partial<Record<OptionalColumn | "title", WorkSort>> = {
  title: "title",
  year: "year",
  added_at: "added",
};

function buildColumns(visible: ReadonlySet<OptionalColumn>): Column<Work>[] {
  const columns: Column<Work>[] = [
    {
      key: "cover",
      header: "",
      width: "3rem",
      render: (work) => (
        <CoverArt workId={work.id} title={work.title} size="thumb" className="h-8 w-8 rounded object-cover" />
      ),
    },
    {
      key: "title",
      header: "Title",
      sortable: true,
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
  ];
  if (visible.has("artist")) {
    columns.push({ key: "artist", header: "Artist", render: (work) => work.artist ?? "" });
  }
  if (visible.has("year")) {
    columns.push({ key: "year", header: "Released", width: "8rem", sortable: true, render: (work) => work.year ?? "" });
  }
  if (visible.has("added_at")) {
    columns.push({
      key: "added_at",
      header: "Added",
      width: "12rem",
      sortable: true,
      render: (work) => new Date(work.added_at).toLocaleDateString(),
    });
  }
  return columns;
}

// The route's own path param (/library/$categoryId/albums, ADR-54) -- no
// longer a URL filter a user picks among several, so it is not in FILTER_KEYS.
// "fresh" is the one real URL filter this list has (todo 4.20, ADR-55): a
// shipped preset, not a generic filter-building UI -- see next.md's own
// scope note on why this list has no media-type/year/language filters yet.
const FILTER_KEYS: readonly string[] = ["fresh"];

export function Library() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  useRecordLastLevel(categoryId, "albums");
  // FILTER_KEYS is empty (category is a route param, not a URL filter), so
  // category_id is injected here rather than read back from the URL -- this
  // is what keeps the query cache key and bulk "select all matching" scoped
  // to the right category (query.key/selection.toRequest both read filters).
  const baseView = useRouteView(FILTER_KEYS);
  const routeView = useMemo<ViewState>(
    () => ({
      query: { ...baseView.query, filters: { ...baseView.query.filters, category_id: categoryId ?? "" } },
      setQuery: baseView.setQuery,
    }),
    [baseView, categoryId],
  );
  const [view, setView] = useState<"table" | "poster">(loadView);
  const frameRef = useRef<HTMLDivElement>(null);
  const [visibleColumns, setVisibleColumns] = useState<OptionalColumn[]>(loadVisibleColumns);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [confirm, setConfirm] = useState<{ monitored: boolean; count: number } | null>(null);
  const queryClient = useQueryClient();

  const narrow = useNarrow();
  const metrics = useGridMetrics(frameRef, POSTER_MIN_WIDTH, POSTER_GAP, POSTER_PADDING);
  const isPoster = view === "poster";
  const list = useLibraryList<Work>(
    {
      name: "works",
      rowHeight: isPoster
        ? metrics.cellWidth + POSTER_LABEL_HEIGHT + POSTER_GAP
        : narrow
          ? CARD_HEIGHT
          : TABLE_ROW_HEIGHT,
      itemsPerRow: isPoster ? metrics.columns : 1,
      idOf: (work) => work.id,
      fetchPage: async ({ cursor, query, signal }) => {
        const sort = query.sort !== null ? SORT_KEYS[query.sort as OptionalColumn | "title"] : undefined;
        const { data, error } = await api.GET("/works", {
          params: {
            query: {
              cursor,
              limit: 100,
              category_id: query.filters.category_id ? Number(query.filters.category_id) : undefined,
              fresh: query.filters.fresh === "1" ? true : undefined,
              sort,
              dir: query.direction === "desc" ? "desc" : undefined,
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

  const visibleSet = useMemo(() => new Set(visibleColumns), [visibleColumns]);
  const columns = useMemo(() => buildColumns(visibleSet), [visibleSet]);

  const toggleColumn = (column: OptionalColumn) => {
    setVisibleColumns((current) => {
      const next = current.includes(column)
        ? current.filter((existing) => existing !== column)
        : [...current, column];
      saveVisibleColumns(next);
      return next;
    });
  };

  // Preview first (section 21.7's consequences preview): "select all
  // matching" never has a client-known count, so the real number a bulk
  // action would touch has to come from the server before it commits.
  const bulkPreview = useMutation({
    mutationFn: async (monitored: boolean) => {
      const request = list.selectionRequest;
      if (!request) return null;
      const { data, error } = await api.POST("/works/bulk/monitor", {
        body: bulkBody(request, monitored, true),
      });
      if (error) throw error;
      return data ?? null;
    },
    onSuccess: (result, monitored) => {
      if (result) setConfirm({ monitored, count: result.count });
    },
  });

  const bulkApply = useMutation({
    mutationFn: async () => {
      const request = list.selectionRequest;
      if (!request || !confirm) return;
      const { error } = await api.POST("/works/bulk/monitor", {
        body: bulkBody(request, confirm.monitored, false),
      });
      if (error) throw error;
    },
    onSuccess: () => {
      setConfirm(null);
      list.clearSelection();
      void queryClient.invalidateQueries({ queryKey: ["works"] });
    },
  });

  return (
    <div ref={frameRef} className="flex h-full flex-col">
      <div className="flex shrink-0 items-center gap-3 border-b border-border-subtle px-4 py-2">
        <h2 className="text-sm font-medium">Albums</h2>

        <div className="ml-auto flex items-center gap-2 text-xs">
          <button
            type="button"
            onClick={() =>
              routeView.setQuery({
                ...routeView.query,
                filters: {
                  ...routeView.query.filters,
                  fresh: routeView.query.filters.fresh === "1" ? "" : "1",
                },
              })
            }
            aria-pressed={routeView.query.filters.fresh === "1"}
            title="Released within the last week"
            className={
              routeView.query.filters.fresh === "1"
                ? "rounded-md border border-accent bg-accent/10 px-2 py-1 text-accent " +
                  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
                : quietButton
            }
          >
            Fresh
          </button>
          {!list.selectionIsEmpty && (
            <>
              <span className="text-ink-muted">
                {list.selectionCount === null ? "All matching" : `${list.selectionCount} selected`}
              </span>
              <button
                type="button"
                disabled={bulkPreview.isPending || bulkApply.isPending}
                onClick={() => bulkPreview.mutate(true)}
                className={quietButton}
              >
                Monitor
              </button>
              <button
                type="button"
                disabled={bulkPreview.isPending || bulkApply.isPending}
                onClick={() => bulkPreview.mutate(false)}
                className={quietButton}
              >
                Unmonitor
              </button>
              <button
                type="button"
                onClick={() => {
                  setConfirm(null);
                  list.clearSelection();
                }}
                className={quietButton}
              >
                Clear
              </button>
            </>
          )}
          <button type="button" onClick={list.selectAllMatching} className={quietButton}>
            Select all matching
          </button>
          <div className="relative">
            <button type="button" onClick={() => setPickerOpen((open) => !open)} className={quietButton}>
              Columns
            </button>
            {pickerOpen && (
              <div
                className="absolute right-0 top-full z-10 mt-1 flex flex-col gap-1 rounded-md border
                           border-border-subtle bg-surface p-2 shadow-lg"
              >
                {OPTIONAL_COLUMNS.map((column) => (
                  <label key={column} className="flex items-center gap-1.5 whitespace-nowrap px-1 py-0.5">
                    <input
                      type="checkbox"
                      checked={visibleSet.has(column)}
                      onChange={() => toggleColumn(column)}
                    />
                    {COLUMN_LABELS[column]}
                  </label>
                ))}
              </div>
            )}
          </div>
          <button
            type="button"
            onClick={() => {
              const next = view === "table" ? "poster" : "table";
              setView(next);
              try {
                localStorage.setItem(VIEW_STORAGE_KEY, next);
              } catch {
                // A per-browser convenience; losing it costs nothing worth failing over.
              }
            }}
            className={quietButton}
          >
            {view === "table" ? "Posters" : "Table"}
          </button>
          {categoryId && (
            <Link
              to="/library/$categoryId/discover"
              params={{ categoryId }}
              className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-accent-ink
                         focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              <Plus aria-hidden className="size-3.5" />
              Add new
            </Link>
          )}
        </div>
      </div>

      {confirm && (
        <div
          className="flex shrink-0 items-center gap-3 border-b border-border-subtle
                     bg-surface px-4 py-2 text-xs"
        >
          <span>
            {confirm.monitored ? "Monitor" : "Unmonitor"} {confirm.count}{" "}
            {confirm.count === 1 ? "work" : "works"}?
          </span>
          <button
            type="button"
            disabled={bulkApply.isPending}
            onClick={() => bulkApply.mutate()}
            className="rounded-md border border-current px-2 py-1 text-accent
                       enabled:hover:bg-accent/10 disabled:cursor-default disabled:opacity-50"
          >
            {bulkApply.isPending ? "Applying…" : "Confirm"}
          </button>
          <button type="button" onClick={() => setConfirm(null)} className={quietButton}>
            Cancel
          </button>
        </div>
      )}

      <div className="min-h-0 flex-1">
        {list.isLoading && <Message>Loading the library…</Message>}
        {list.error !== null && <Message>The library could not be read.</Message>}
        {!list.isLoading && list.error === null && list.items.length === 0 && (
          <Message>
            Nothing in the library yet. Identified files appear here once a scan finds them.
          </Message>
        )}

        {list.items.length > 0 &&
          (view === "table" ? (
            <ListTable list={list} columns={columns} idOf={(work) => work.id} />
          ) : (
            <PosterGrid
              list={list}
              idOf={(work) => work.id}
              renderPoster={(work) => (
                <Poster
                  subject="works"
                  imageId={work.id}
                  title={work.title}
                  subtitle={[work.artist ?? work.media_type, work.year].filter(Boolean).join(" · ")}
                  link={{ to: "/works/$id", params: { id: String(work.id) } }}
                />
              )}
            />
          ))}
      </div>
    </div>
  );
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}

const quietButton =
  "rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
