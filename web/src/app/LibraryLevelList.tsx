import { useEffect, useRef, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import type { LinkProps } from "@tanstack/react-router";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { ArrowDown, ArrowUp, Check, Columns3, Plus } from "lucide-react";

import { api } from "@/api/client";
import { albumStatusFill, albumStatusLabel } from "@/lib/albumStatus";
import { usePreference } from "@/lib/usePreference";
import { useLibraryList } from "@/list/useLibraryList";
import { useGridMetrics } from "@/list/useGridMetrics";
import { PosterGrid } from "@/list/PosterGrid";
import { useRouteView } from "@/list/useRouteView";
import { useRecordLastLevel } from "@/list/useRecordLastLevel";
import type { components } from "@/api/schema";
import { LEVEL_FILTER_FIELDS } from "./filterFields";
import { CoverArt } from "./CoverArt";
import { CustomFilterMenu } from "./CustomFilterMenu";
import {
  DEFAULT_POSTER_OPTIONS, IndexPoster, IndexViewToggle, OVERVIEW_ROW_HEIGHT, OverviewList, POSTER_GAP, POSTER_PADDING,
  POSTER_WIDTHS, PosterOptionsMenu, RowActions, posterRowHeight, type IndexEntry, type IndexView, type PosterOptions,
} from "./IndexViews";
import { formatBytes } from "./Queue";

type LevelItem = components["schemas"]["LibraryLevelItem"];

const ROW_HEIGHT = 44;
const FILTER_KEYS: readonly string[] = ["monitored", "where"];


// The poster wall's own geometry: a tile is at least this wide, gaps and page
// padding are the grid's, and a row is one square cover plus its two lines of
// label.


/** Which levels open as a poster wall rather than a list -- the browse views
 *  a person actually looks at (Lidarr's own Artists and Albums), not Tracks,
 *  where a cover per row says nothing a tracklist does not. */
const POSTER_LEVELS = new Set(["artists", "albums"]);

// What each level can be ordered by, and which way round the API wants it.
// Title is every level's default; the rest are what the works list already
// indexes (migration 0016), so no order here costs a sort (2026-09-19).
type SortKey = "title" | "artist" | "year" | "added";

interface SortEntry {
  label: string;
  dir?: "asc" | "desc";
  // levels absent means every level offers this order.
  levels?: readonly string[];
}

const SORTS: Record<SortKey, SortEntry> = {
  title: { label: "Name" },
  artist: { label: "Artist", levels: ["albums"] },
  year: { label: "Year", dir: "desc", levels: ["albums"] },
  added: { label: "Recently added", dir: "desc", levels: ["albums"] },
};

function SortPicker({
  level,
  sort,
  onChange,
}: {
  level: string | undefined;
  sort: SortKey;
  onChange: (sort: SortKey) => void;
}) {
  const available = (Object.entries(SORTS) as [SortKey, SortEntry][]).filter(
    ([, entry]) => entry.levels == null || (level != null && entry.levels.includes(level)),
  );
  if (available.length < 2) return null;
  return (
    <label className="flex items-center gap-1 text-xs text-ink-muted">
      Sort
      <select
        value={sort}
        onChange={(event) => onChange(event.target.value as SortKey)}
        className="rounded-md border border-border-subtle bg-canvas px-1.5 py-1 text-xs text-ink
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        {available.map(([key, entry]) => (
          <option key={key} value={key}>
            {entry.label}
          </option>
        ))}
      </select>
    </label>
  );
}

type ViewMode = IndexView;
const VIEW_STORAGE_KEY = "solarr.level.view";

/** The remembered poster/list choice per level, so switching to the list on
 *  Tracks does not also flatten the Artists wall. */
function useViewMode(level: string | undefined): [ViewMode, (mode: ViewMode) => void] {
  const fallback: ViewMode = level && POSTER_LEVELS.has(level) ? "poster" : "list";
  const [mode, setMode] = useState<ViewMode>(fallback);

  useEffect(() => {
    try {
      const raw = localStorage.getItem(`${VIEW_STORAGE_KEY}.${level ?? ""}`);
      setMode(raw === "poster" || raw === "overview" || raw === "list" ? raw : fallback);
    } catch {
      setMode(fallback);
    }
  }, [level, fallback]);

  const choose = (next: ViewMode) => {
    setMode(next);
    try {
      localStorage.setItem(`${VIEW_STORAGE_KEY}.${level ?? ""}`, next);
    } catch {
      // A browser refusing storage costs the memory of the choice, nothing more.
    }
  };

  return [mode, choose];
}

/** Which /{subject}/{id}/artwork this row's own image comes from, or null
 *  for a level with no image source at all -- Artists reads the person's
 *  own id directly; Tracks reads its own album's work_id, so a song falls
 *  back to its album's cover (which itself falls back server-side to the
 *  artist's, GetWorkArtwork's own chain). */
function imageSourceFor(level: string | undefined, item: LevelItem): { subject: "works" | "people"; id: number } | null {
  if (level === "artists") return { subject: "people", id: item.id };
  if (item.work_id != null) return { subject: "works", id: item.work_id };
  return null;
}

/** Where a row navigates: Artists to the person page, Tracks to their own
 *  album (there is no track-detail page -- a track is a work_part shown
 *  inside its work, WorkDetail's own Parts section). */
/** A level row as the index views show it. */
function entryOf(level: string | undefined, item: LevelItem): IndexEntry {
  return {
    id: item.id, kind: level === "artists" ? "artists" : level === "albums" ? "albums" : "tracks",
    title: item.title, subtitle: item.subtitle, year: item.year, status: item.status,
    presentCount: item.present_count, requiredCount: item.required_count, monitored: item.monitored,
    overview: item.overview, sizeBytes: item.size_bytes, artistCover: item.artist_cover,
    image: imageSourceFor(level, item), link: linkFor(level, item),
  };
}

function linkFor(level: string | undefined, item: LevelItem): LinkProps | null {
  if (level === "artists") return { to: "/people/$id", params: { id: String(item.id) } };
  if (item.work_id != null) return { to: "/works/$id", params: { id: String(item.work_id) } };
  return null;
}

/** The browse view for any navigation level (ADR-54). Artists and Albums
 *  open as a poster wall; every level can be switched to a compact list. */
export function LibraryLevelList() {
  const { categoryId, level } = useParams({ strict: false }) as {
    categoryId?: string;
    level?: string;
  };
  useRecordLastLevel(categoryId, level ?? "");

  const [view, setView] = useViewMode(level);
  const [sort, setSort] = usePreference<SortKey>(`level.sort.${level ?? ""}`, "title");
  const [storedDir, setDir] = usePreference<"asc" | "desc" | "">(`level.dir.${level ?? ""}`, "");
  const dir = storedDir || SORTS[sort].dir || "asc";
  const [hidden, setHidden] = usePreference<string[]>(`level.hiddenColumns.${level ?? ""}`, []);
  // A header clicked again turns its order round, as in Lidarr.
  const sortBy = (key: SortKey) => {
    if (key === sort) setDir(dir === "asc" ? "desc" : "asc");
    else {
      setSort(key);
      setDir("");
    }
  };
  const [storedOptions, setOptions] = usePreference<PosterOptions>("posters.options", DEFAULT_POSTER_OPTIONS);
  const options = { ...DEFAULT_POSTER_OPTIONS, ...storedOptions };
  const frameRef = useRef<HTMLDivElement>(null);
  const metrics = useGridMetrics(frameRef, POSTER_WIDTHS[options.size], POSTER_GAP, POSTER_PADDING);
  const isPoster = view === "poster";


  const list = useLibraryList<LevelItem>(
    {
      name: `library-level:${categoryId ?? ""}:${level ?? ""}:${sort}:${dir}`,
      rowHeight: isPoster ? posterRowHeight(options, metrics.cellWidth) : view === "overview" ? OVERVIEW_ROW_HEIGHT : ROW_HEIGHT,
      itemsPerRow: isPoster ? metrics.columns : 1,
      idOf: (item) => item.id,
      fetchPage: async ({ cursor, query, signal }) => {
        const monitored = query.filters.monitored;
        const { data, error } = await api.GET("/libraries/{id}/{level}", {
          params: {
            path: { id: Number(categoryId), level: level ?? "" },
            query: { cursor, limit: 100, sort, dir, monitored: monitored ? monitored === "true" : undefined, where: query.filters.where || undefined },
          },
          signal,
        });
        if (error) throw error;
        return data;
      },
    },
    useRouteView(FILTER_KEYS),
  );

  return (
    <div ref={frameRef} className="flex h-full flex-col">
      <div className="flex flex-wrap items-center gap-2 border-b border-border-subtle px-4 py-2">
        <h1 className="text-sm font-semibold capitalize tracking-tight">{level ?? "Library"}</h1>
        <div className="ml-auto flex items-center gap-2">
          {level === "artists" && (
            <label className="flex items-center gap-1 text-xs text-ink-muted">
              Show
              <select
                value={list.query.filters.monitored ?? ""}
                onChange={(event) => list.filterBy("monitored", event.target.value)}
                className="rounded-md border border-border-subtle bg-canvas px-1.5 py-1 text-xs text-ink"
              >
                <option value="">All artists</option>
                <option value="true">Monitored</option>
                <option value="false">Unmonitored</option>
              </select>
            </label>
          )}
          {level && LEVEL_FILTER_FIELDS[level] && (
            <CustomFilterMenu
              fields={LEVEL_FILTER_FIELDS[level]}
              storageKey={`${categoryId ?? ""}.${level}`}
              where={list.query.filters.where ?? ""}
              onChange={(where) => list.filterBy("where", where)}
            />
          )}
          {view === "list" ? (
            <ColumnChooser level={level} hidden={hidden} onChange={setHidden} />
          ) : (
            <>
              <SortPicker level={level} sort={sort} onChange={sortBy} />
              {isPoster && <PosterOptionsMenu options={options} onChange={setOptions} />}
            </>
          )}
          <IndexViewToggle view={view} onChange={setView} />
          {categoryId && (
            <Link
              to="/library/$categoryId/discover"
              params={{ categoryId }}
              className="ml-2 flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink
                         focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              <Plus aria-hidden className="size-4" />
              Add new
            </Link>
          )}
        </div>
      </div>

      {list.isLoading && <Message>Loading…</Message>}
      {list.error !== null && (
        <Message>{(list.error as { detail?: string }).detail ?? "This view could not be read."}</Message>
      )}
      {!list.isLoading && list.error === null && list.items.length === 0 && <Message>Nothing here yet.</Message>}

      {list.items.length > 0 && (
        <div className="min-h-0 flex-1">
          {isPoster ? (
            <PosterGrid
              list={list}
              idOf={(item) => item.id}
              renderPoster={(item) => <IndexPoster entry={entryOf(level, item)} options={options} />}
            />
          ) : view === "overview" ? (
            <OverviewList list={list} entryOf={(item) => entryOf(level, item)} />
          ) : (
            <LevelTable list={list} level={level} sort={sort} dir={dir} onSort={sortBy} hidden={hidden} />
          )}
        </div>
      )}
    </div>
  );
}

type OptionalColumn = "subtitle" | "year" | "progress" | "status" | "size" | "monitored" | "actions";

interface LevelColumns {
  title: { header: string; sort?: SortKey };
  optional: readonly { key: OptionalColumn; header: string; sort?: SortKey; width: string }[];
}

// What each level's table shows, and which headers sort (the orders the API has for that level).
const LEVEL_COLUMNS: Record<string, LevelColumns> = {
  artists: {
    title: { header: "Artist", sort: "title" },
    optional: [
      { key: "progress", header: "Albums", width: "7rem" },
      { key: "status", header: "Status", width: "6rem" },
      { key: "actions", header: "Actions", width: "5rem" },
    ],
  },
  albums: {
    title: { header: "Album", sort: "title" },
    optional: [
      { key: "subtitle", header: "Artist", sort: "artist", width: "1fr" },
      { key: "year", header: "Released", sort: "year", width: "6rem" },
      { key: "progress", header: "Tracks", width: "6rem" },
      { key: "status", header: "Status", width: "6rem" },
      { key: "size", header: "Size", width: "6rem" },
      { key: "monitored", header: "Monitored", width: "6rem" },
      { key: "actions", header: "Actions", width: "5rem" },
    ],
  },
  tracks: {
    title: { header: "Track" },
    optional: [
      { key: "subtitle", header: "Artist -- Album", sort: "artist", width: "1.5fr" },
      { key: "year", header: "Released", width: "6rem" },
    ],
  },
};

const FALLBACK_COLUMNS: LevelColumns = {
  title: { header: "Title", sort: "title" },
  optional: [
    { key: "subtitle", header: "", width: "1fr" },
    { key: "year", header: "Year", width: "6rem" },
  ],
};

function columnsFor(level: string | undefined): LevelColumns {
  return (level && LEVEL_COLUMNS[level]) || FALLBACK_COLUMNS;
}

/** The small config button beside a table: which of its columns to show. */
function ColumnChooser({
  level,
  hidden,
  onChange,
}: {
  level: string | undefined;
  hidden: readonly string[];
  onChange: (hidden: string[]) => void;
}) {
  const { optional } = columnsFor(level);
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger
        aria-label="Choose columns"
        title="Choose columns"
        className="rounded-md border border-border-subtle p-1.5 text-ink-muted hover:text-ink"
      >
        <Columns3 aria-hidden className="size-4" />
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          sideOffset={4}
          className="z-50 min-w-40 rounded-md border border-border-subtle bg-surface p-1 text-sm shadow-lg"
        >
          {optional.map((column) => (
            <DropdownMenu.CheckboxItem
              key={column.key}
              checked={!hidden.includes(column.key)}
              onSelect={(event) => event.preventDefault()}
              onCheckedChange={(shown) =>
                onChange(shown ? hidden.filter((key) => key !== column.key) : [...hidden, column.key])
              }
              className="flex cursor-default items-center gap-2 rounded px-2 py-1 outline-none data-[highlighted]:bg-canvas"
            >
              <span className="size-3.5">
                <DropdownMenu.ItemIndicator>
                  <Check aria-hidden className="size-3.5" />
                </DropdownMenu.ItemIndicator>
              </span>
              {column.header || "Details"}
            </DropdownMenu.CheckboxItem>
          ))}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

function LevelTable({
  list,
  level,
  sort,
  dir,
  onSort,
  hidden,
}: {
  list: ReturnType<typeof useLibraryList<LevelItem>>;
  level: string | undefined;
  sort: SortKey;
  dir: "asc" | "desc";
  onSort: (key: SortKey) => void;
  hidden: readonly string[];
}) {
  const { title, optional } = columnsFor(level);
  const shown = optional.filter((column) => !hidden.includes(column.key));
  const template = ["2rem", "1.5fr", ...shown.map((column) => column.width)].join(" ");
  const header = (label: string, key: SortKey | undefined) =>
    key ? (
      <button type="button" onClick={() => onSort(key)} className="flex items-center gap-1 text-left hover:text-ink">
        {label}
        {sort === key && (dir === "asc" ? <ArrowUp aria-hidden className="size-3" /> : <ArrowDown aria-hidden className="size-3" />)}
      </button>
    ) : (
      <span>{label}</span>
    );

  return (
    <div className="flex h-full flex-col">
      <div
        role="row"
        className="grid shrink-0 items-center gap-3 border-b border-border-subtle bg-surface px-4 py-2 text-xs font-medium text-ink-muted"
        style={{ gridTemplateColumns: template }}
      >
        <span />
        {header(title.header, title.sort)}
        {shown.map((column) => (
          <span key={column.key}>{header(column.header, column.sort)}</span>
        ))}
      </div>
      <div ref={list.scrollRef} className="min-h-0 flex-1 overflow-auto">
        <div style={{ height: list.totalSize, position: "relative" }}>
          {list.virtualItems.map((virtual) => {
            const item = list.items[virtual.index];
            if (!item) return null;
            const image = imageSourceFor(level, item);
            const link = linkFor(level, item);
            const rowClass = "absolute inset-x-0 grid items-center gap-3 border-b border-border-subtle px-4 text-sm";
            const style = {
              height: virtual.size,
              transform: `translateY(${virtual.start}px)`,
              gridTemplateColumns: template,
            };
            const cells = (
              <>
                {image ? (
                  <CoverArt
                    subject={image.subject}
                    workId={image.id}
                    title={item.title}
                    size="thumb"
                    className={`h-8 w-8 object-cover ${image.subject === "people" ? "rounded-full" : "rounded"}`}
                  />
                ) : (
                  <span />
                )}
                <span className="truncate font-medium">{item.title}</span>
                {shown.map((column) => (
                  <span key={column.key} className="truncate text-ink-muted">
                    {cellFor(column.key, item, level)}
                  </span>
                ))}
              </>
            );
            return link ? (
              <Link key={item.id} {...link} className={`${rowClass} hover:bg-surface`} style={style}>
                {cells}
              </Link>
            ) : (
              <div key={item.id} className={rowClass} style={style}>
                {cells}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

function cellFor(column: OptionalColumn, item: LevelItem, level?: string): React.ReactNode {
  switch (column) {
    case "size":
      return item.size_bytes ? formatBytes(item.size_bytes) : "";
    case "monitored":
    case "actions":
      return <RowActions column={column} entry={entryOf(level, item)} />;
    case "subtitle":
      return item.subtitle ?? "";
    case "year":
      return item.year ?? "";
    case "progress":
      return item.required_count != null ? `${item.present_count ?? 0} / ${item.required_count}` : "";
    case "status":
      return item.status ? (
        <span className="flex items-center gap-1.5">
          <span className={`size-2 shrink-0 rounded-full ${albumStatusFill(item.status)}`} />
          {albumStatusLabel(item.status)}
        </span>
      ) : (
        ""
      );
  }
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
