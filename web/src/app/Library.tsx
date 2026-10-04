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
import type { components, paths } from "@/api/schema";
import { albumStatusLabel } from "@/lib/albumStatus";
import { CoverArt } from "./CoverArt";
import { CustomFilterMenu } from "./CustomFilterMenu";
import { LEVEL_FILTER_FIELDS } from "./filterFields";
import { BulkEditDialog } from "./EditAlbum";
import {
  DEFAULT_POSTER_OPTIONS, IndexPoster, IndexViewToggle, OVERVIEW_ROW_HEIGHT, OverviewList, POSTER_GAP, POSTER_PADDING,
  POSTER_WIDTHS, PosterOptionsMenu, RowActions, posterRowHeight, workEntry, type IndexView, type PosterOptions,
} from "./IndexViews";
import { formatBytes } from "./Queue";
import { usePreference } from "@/lib/usePreference";

type Work = components["schemas"]["Work"];

// The request body isn't a named schema component (it's inline in
// openapi.yaml), so openapi-typescript has nothing to hang a name on --
// spelled out directly rather than reached for through api.POST's own types.
interface BulkMonitorBody {
  ids?: number[];
  category_id?: number;
  excluded_ids?: number[];
  fresh?: boolean;
  monitored: boolean;
  preview?: boolean;
}

// Selection is a query, never a fetched list of ids (section 21.7) -- the
// request carries either explicit ids or the current filter plus what the
// user deselected from "select all matching".
function selectionBody(request: SelectionRequest) {
  if (request.kind === "ids") return { ids: request.ids };
  const categoryId = request.filters.category_id;
  // The list's own filters ride along, or "select all matching" under Fresh would act on every album.
  return {
    category_id: categoryId ? Number(categoryId) : undefined,
    excluded_ids: request.excluded,
    fresh: request.filters.fresh === "1" ? true : undefined,
    where: request.filters.where || undefined,
  };
}

function bulkBody(request: SelectionRequest, monitored: boolean, preview: boolean): BulkMonitorBody {
  return { ...selectionBody(request), monitored, preview };
}
type WorkSort = NonNullable<NonNullable<paths["/works"]["get"]["parameters"]["query"]>["sort"]>;

// Title plus a year/media-type line, padding included.
const CARD_HEIGHT = 68;
// A 32px thumbnail plus padding, for a table row.
const TABLE_ROW_HEIGHT = 44;
// The poster wall's own geometry, matching LibraryLevelList's.
const VIEW_STORAGE_KEY = "solarr.library.view";

/** Albums open as a poster wall (Lidarr's own album view, 2026-09-13); Radarr's Overview and the
 *  table are a click away and remembered once chosen. */
function loadView(): IndexView {
  try {
    const stored = localStorage.getItem(VIEW_STORAGE_KEY);
    return stored === "table" || stored === "list" ? "list" : stored === "overview" ? "overview" : "poster";
  } catch {
    return "poster";
  }
}

const entryOf = workEntry;

// Cover and title are always shown -- they are how a row is identified at
// all. The rest is what section 21's "configurable columns" picks among.
const OPTIONAL_COLUMNS = ["artist", "year", "added_at", "size", "status", "monitored", "actions"] as const;
type OptionalColumn = (typeof OPTIONAL_COLUMNS)[number];
const COLUMN_LABELS: Record<OptionalColumn, string> = {
  artist: "Artist",
  year: "Released",
  added_at: "Added",
  size: "Size",
  status: "Status",
  monitored: "Monitored",
  actions: "Actions",
};
const COLUMNS_STORAGE_KEY = "solarr.library.columns";

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

// Maps a column key to the sort value the /works API understands.
const SORT_KEYS: Record<string, WorkSort> = {
  title: "title",
  artist: "artist",
  year: "year",
  added_at: "added",
  size: "size",
  status: "status",
  profile: "profile",
  genre: "genre",
  tag: "tag",
};

// The Sort menu: Radarr's own list of what a view orders by.
const SORT_CHOICES: readonly (readonly [string, string])[] = [
  ["artist", "Artist"],
  ["title", "Title"],
  ["year", "Released"],
  ["added_at", "Added"],
  ["size", "Size on disk"],
  ["profile", "Quality profile"],
  ["status", "Status"],
  ["genre", "Genres"],
  ["tag", "Tags"],
];

// The poster wall's artist headers: room for one line of heading above a row.
const POSTER_GROUP_HEADER_HEIGHT = 44;

const artistOf = (work: Work) => work.artist ?? "Unknown artist";

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
          {work.unverified && <span className="ml-2 text-xs text-ink-muted">unverified</span>}
        </Link>
      ),
    },
  ];
  if (visible.has("artist")) {
    columns.push({ key: "artist", header: "Artist", sortable: true, render: (work) => work.artist ?? "" });
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
  if (visible.has("size")) {
    columns.push({
      key: "size",
      header: "Size",
      width: "6rem",
      sortable: true,
      render: (work) => (work.size_bytes ? formatBytes(work.size_bytes) : ""),
    });
  }
  if (visible.has("status")) {
    columns.push({
      key: "status",
      header: "Status",
      width: "8rem",
      sortable: true,
      render: (work) => (work.status ? albumStatusLabel(work.status) : ""),
    });
  }
  if (visible.has("monitored")) {
    columns.push({ key: "monitored", header: "", width: "3rem", render: (work) => <RowActions column="monitored" entry={entryOf(work)} /> });
  }
  if (visible.has("actions")) {
    columns.push({ key: "actions", header: "", width: "5rem", render: (work) => <RowActions column="actions" entry={entryOf(work)} /> });
  }
  return columns;
}

// The route's own path param (/library/$categoryId/albums, ADR-54) -- no
// longer a URL filter a user picks among several, so it is not in FILTER_KEYS.
// "fresh" is the one real URL filter this list has (todo 4.20, ADR-55): a
// shipped preset, not a generic filter-building UI -- see next.md's own
// scope note on why this list has no media-type/year/language filters yet.
const FILTER_KEYS: readonly string[] = ["fresh", "where"];

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
  const [view, setView] = useState<IndexView>(loadView);
  const [storedOptions, setOptions] = usePreference<PosterOptions>("posters.options", DEFAULT_POSTER_OPTIONS);
  const options = { ...DEFAULT_POSTER_OPTIONS, ...storedOptions };
  const frameRef = useRef<HTMLDivElement>(null);
  const [visibleColumns, setVisibleColumns] = useState<OptionalColumn[]>(loadVisibleColumns);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [confirm, setConfirm] = useState<{ monitored: boolean; count: number } | null>(null);
  const [editing, setEditing] = useState(false);
  const queryClient = useQueryClient();

  const narrow = useNarrow();
  const metrics = useGridMetrics(frameRef, POSTER_WIDTHS[options.size], POSTER_GAP, POSTER_PADDING);
  const isPoster = view === "poster";
  // Albums as posters are grouped by artist (asked for 2026-09-17), which only
  // works if they arrive ordered by artist, whatever the table was sorted by.
  // Any other sort the user picks shows the wall flat, in that order.
  const groupedByArtist = isPoster && (routeView.query.sort === null || routeView.query.sort === "artist");
  const listView = useMemo<ViewState>(
    () =>
      isPoster && groupedByArtist
        ? { ...routeView, query: { ...routeView.query, sort: "artist", direction: "asc" } }
        : routeView,
    [isPoster, groupedByArtist, routeView],
  );
  const list = useLibraryList<Work>(
    {
      name: "works",
      groupOf: groupedByArtist ? artistOf : undefined,
      groupHeaderHeight: POSTER_GROUP_HEADER_HEIGHT,
      rowHeight: isPoster
        ? posterRowHeight(options, metrics.cellWidth)
        : view === "overview"
          ? OVERVIEW_ROW_HEIGHT
          : narrow
            ? CARD_HEIGHT
            : TABLE_ROW_HEIGHT,
      itemsPerRow: isPoster ? metrics.columns : 1,
      idOf: (work) => work.id,
      fetchPage: async ({ cursor, query, signal }) => {
        const sort = query.sort !== null ? SORT_KEYS[query.sort] : undefined;
        const { data, error } = await api.GET("/works", {
          params: {
            query: {
              cursor,
              limit: 100,
              category_id: query.filters.category_id ? Number(query.filters.category_id) : undefined,
              fresh: query.filters.fresh === "1" ? true : undefined,
              where: query.filters.where || undefined,
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
    listView,
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
          <CustomFilterMenu
            fields={LEVEL_FILTER_FIELDS.albums!}
            storageKey={`${categoryId ?? ""}.albums`}
            where={routeView.query.filters.where ?? ""}
            onChange={(where) => routeView.setQuery({ ...routeView.query, filters: { ...routeView.query.filters, where } })}
          />
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
              <button type="button" onClick={() => setEditing(true)} className={quietButton}>
                Edit
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
          <label className="flex items-center gap-1 text-ink-muted">
            Sort
            <select
              value={routeView.query.sort ?? "artist"}
              onChange={(event) => routeView.setQuery({ ...routeView.query, sort: event.target.value })}
              className="rounded-md border border-border-subtle bg-surface px-1.5 py-1 text-ink"
            >
              {SORT_CHOICES.map(([key, label]) => (
                <option key={key} value={key}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            onClick={() =>
              routeView.setQuery({
                ...routeView.query,
                sort: routeView.query.sort ?? "artist",
                direction: routeView.query.direction === "desc" ? "asc" : "desc",
              })
            }
            title="Reverse the order"
            className={quietButton}
          >
            {routeView.query.direction === "desc" ? "Descending" : "Ascending"}
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
          {isPoster && <PosterOptionsMenu options={options} onChange={setOptions} />}
          <IndexViewToggle
            view={view}
            onChange={(next) => {
              setView(next);
              try {
                localStorage.setItem(VIEW_STORAGE_KEY, next);
              } catch {
                // A per-browser convenience; losing it costs nothing worth failing over.
              }
            }}
          />
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

      {editing && list.selectionRequest && (
        <BulkEditDialog
          selection={selectionBody(list.selectionRequest)}
          onClose={() => setEditing(false)}
          onDone={() => {
            setEditing(false);
            list.clearSelection();
            void queryClient.invalidateQueries({ queryKey: ["works"] });
            void queryClient.invalidateQueries({ queryKey: ["tags"] });
          }}
        />
      )}

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
          (view === "list" ? (
            <ListTable list={list} columns={columns} idOf={(work) => work.id} />
          ) : view === "overview" ? (
            <OverviewList list={list} entryOf={entryOf} />
          ) : (
            <PosterGrid
              list={list}
              idOf={(work) => work.id}
              renderPoster={(work) => <IndexPoster entry={entryOf(work)} options={options} />}
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
