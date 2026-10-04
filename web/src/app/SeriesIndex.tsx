import { useMemo, useRef, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { Bookmark, BookmarkCheck, Plus, Search, Tv } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { albumStatusLabel, type AlbumStatus } from "@/lib/albumStatus";
import { remoteImage } from "@/lib/remoteImage";
import { useLibrary } from "@/lib/useLibrary";
import { usePreference } from "@/lib/usePreference";
import { PosterGrid } from "@/list/PosterGrid";
import { ListTable, type Column } from "@/list/ListTable";
import { useGridMetrics } from "@/list/useGridMetrics";
import { useLibraryList } from "@/list/useLibraryList";
import { useNarrow } from "@/list/useNarrow";
import { useRouteView } from "@/list/useRouteView";

import {
  DEFAULT_POSTER_OPTIONS, IndexViewToggle, OVERVIEW_ROW_HEIGHT, POSTER_GAP, POSTER_PADDING, POSTER_WIDTHS, PosterOptionsMenu, posterRowHeight,
  type IndexView, type PosterOptions,
} from "./IndexViews";
import { CustomFilterMenu } from "./CustomFilterMenu";
import { Poster } from "./Poster";
import { parseWhere, passes, type FilterField } from "@/list/customFilter";

type Series = components["schemas"]["SeriesSummary"];

const FILTER_KEYS: readonly string[] = ["q", "status", "network", "genre", "profile", "monitored", "missing", "where"];
const POSTER_RATIO = 1.5;

type SortKey = "title" | "year" | "added" | "network" | "next" | "progress";

const SORTS: Record<SortKey, { label: string; compare: (a: Series, b: Series) => number }> = {
  title: { label: "Title", compare: (a, b) => a.sort_title.localeCompare(b.sort_title) },
  year: { label: "Year", compare: (a, b) => (b.year ?? 0) - (a.year ?? 0) },
  added: { label: "Recently added", compare: (a, b) => b.added_at - a.added_at },
  network: { label: "Network", compare: (a, b) => (a.network ?? "~").localeCompare(b.network ?? "~") },
  next: { label: "Next airing", compare: (a, b) => (a.next_airing ?? "9999").localeCompare(b.next_airing ?? "9999") },
  progress: { label: "Episodes missing", compare: (a, b) => b.counts.aired_missing - a.counts.aired_missing },
};

/** The bar under a poster in the vocabulary every library shares: grey unmonitored, red missing, green complete, yellow still to air. */
export function seriesStatus(series: Pick<Series, "counts" | "monitored">): AlbumStatus {
  const { monitored, present, aired_missing: missing } = series.counts;
  if (!series.monitored || monitored === 0) return "unverified";
  if (missing > 0) return "incomplete";
  if (present >= monitored) return "complete";
  return "unreleased";
}

/** Sonarr's progress bar colour, kept for the Networks page's own tiles. */
export function progressTone(series: Pick<Series, "counts" | "monitored">): { fill: string; label: string } {
  const status = seriesStatus(series);
  const fill = { complete: "bg-ok", incomplete: "bg-danger", downloading: "bg-busy", unreleased: "bg-accent", unverified: "bg-ink-muted" }[status];
  return { fill, label: albumStatusLabel(status) };
}

async function fetchAll(categoryId: string): Promise<Series[]> {
  const all: Series[] = [];
  let cursor: string | undefined;
  do {
    const { data, error } = await api.GET("/series", { params: { query: { category_id: Number(categoryId), limit: 200, ...(cursor ? { cursor } : {}) } } });
    if (error) throw error;
    all.push(...data.items);
    cursor = data.next_cursor || undefined;
  } while (cursor);
  return all;
}

/** Every series of a library, read once and kept: a library of a thousand series is five requests of two hundred. */
function loadAll(queryClient: QueryClient, categoryId: string): Promise<Series[]> {
  return queryClient.fetchQuery({ queryKey: ["series", categoryId, "all"], staleTime: 15_000, queryFn: () => fetchAll(categoryId) });
}

/** What a person can build a custom filter from; each reads one value (or several, for genres) of a series. */
const SERIES_FILTER_FIELDS: readonly FilterField[] = [
  { key: "title", label: "Title", kind: "text" },
  { key: "year", label: "Year", kind: "number" },
  { key: "network", label: "Network", kind: "text" },
  { key: "genre", label: "Genre", kind: "text" },
  { key: "status", label: "Status", kind: "enum", choices: [["continuing", "Continuing"], ["ended", "Ended"]] },
  { key: "monitored", label: "Monitored", kind: "bool" },
  { key: "present", label: "Episodes on disk", kind: "number" },
  { key: "missing", label: "Aired and missing episodes", kind: "number" },
  { key: "seasons", label: "Seasons", kind: "number" },
  { key: "days_since_added", label: "Days since added", kind: "number" },
];

function seriesValues(series: Series, field: string): (string | number | boolean | undefined)[] {
  switch (field) {
    case "title": return [series.title];
    case "year": return [series.year];
    case "network": return [series.network];
    case "genre": return series.genres ?? [];
    case "status": return [series.status];
    case "monitored": return [series.monitored];
    case "present": return [series.counts.present];
    case "missing": return [series.counts.aired_missing];
    case "seasons": return [(series.season_states ?? []).filter((season) => season.number > 0).length];
    case "days_since_added": return [Math.floor((Date.now() / 1000 - series.added_at) / 86400)];
    default: return [];
  }
}

function matching(all: Series[], filters: Readonly<Record<string, string>>): Series[] {
  const needle = (filters.q ?? "").trim().toLowerCase();
  const conditions = parseWhere(filters.where);
  return all
    .filter((series) => passes(conditions, SERIES_FILTER_FIELDS, (field) => seriesValues(series, field)))
    .filter((series) => needle === "" || series.title.toLowerCase().includes(needle))
    .filter((series) => !filters.network || series.network === filters.network)
    .filter((series) => !filters.genre || (series.genres ?? []).includes(filters.genre))
    .filter((series) => !filters.profile || series.quality_profile_id === Number(filters.profile))
    .filter((series) => !filters.status || series.status === filters.status)
    .filter((series) => filters.missing !== "1" || series.counts.aired_missing > 0)
    .filter((series) => filters.monitored !== "1" || series.monitored);
}

/** The monitored bookmark of a poster or row: Radarr's, in the same corner. */
function MonitorBookmark({ series, categoryId }: { series: Series; categoryId: string }) {
  const queryClient = useQueryClient();
  const set = useMutation({
    mutationFn: async () => {
      const { error } = await api.PUT("/series/{id}/monitoring", {
        params: { path: { id: series.id }, query: { category_id: Number(categoryId) } },
        body: { option: series.monitored ? "none" : "existing" },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["series", categoryId, "all"] });
    },
  });
  const Icon = series.monitored ? BookmarkCheck : Bookmark;
  const label = series.monitored ? "Monitored: click to stop" : "Not monitored: click to monitor the episodes on disk";
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      disabled={set.isPending}
      onClick={(event) => {
        event.preventDefault();
        event.stopPropagation();
        set.mutate();
      }}
      className={`flex size-7 items-center justify-center rounded-full border border-border-subtle bg-canvas/85 shadow-sm hover:text-ink disabled:opacity-50 ${series.monitored ? "text-accent" : "text-ink-muted"}`}
    >
      <Icon aria-hidden className="size-4" />
    </button>
  );
}

/** One series as a tile of the same wall the music library uses: the poster, its title and what is on disk. */
export function SeriesPosterTile({ series, categoryId, options = DEFAULT_POSTER_OPTIONS }: { series: Series; categoryId: string; options?: PosterOptions }) {
  return (
    <Poster
      subject="works"
      imageId={null}
      imageSrc={series.poster_url ? remoteImage(series.poster_url, "small") : undefined}
      portrait
      title={series.title}
      subtitle={[series.year, series.network, `${series.counts.present}/${series.counts.monitored}`].filter(Boolean).join(" · ")}
      link={{ to: "/library/$categoryId/series/$seriesId", params: { categoryId, seriesId: String(series.id) } }}
      topLeft={options.showMonitored ? <MonitorBookmark series={series} categoryId={categoryId} /> : undefined}
      showTitle={options.showTitle}
      status={options.showAvailability ? seriesStatus(series) : undefined}
      presentCount={series.counts.present}
      requiredCount={series.counts.monitored}
    />
  );
}

/** Radarr's overview, as the music library draws it: a cover, the name, a paragraph and the details, a row each. */
function SeriesOverviewList({ list, categoryId }: { list: ReturnType<typeof useLibraryList<Series>>; categoryId: string }) {
  return (
    <div ref={list.scrollRef} className="h-full overflow-auto">
      <div style={{ height: list.totalSize, position: "relative" }}>
        {list.virtualItems.map((virtual) => {
          const series = list.items[virtual.index];
          if (!series) return null;
          const details = [series.year, series.network, series.status === "ended" ? "Ended" : "Continuing", `${series.counts.present} of ${series.counts.monitored} episodes`, ...(series.genres ?? []).slice(0, 3)].filter(Boolean);
          return (
            <div key={series.id} className="absolute inset-x-0 px-4" style={{ height: virtual.size, transform: `translateY(${virtual.start}px)` }}>
              <Link
                to="/library/$categoryId/series/$seriesId"
                params={{ categoryId, seriesId: String(series.id) }}
                className="block h-full hover:bg-surface"
              >
                <div className="flex h-full gap-4 border-b border-border-subtle py-3">
                  <div className="relative h-full shrink-0 overflow-hidden rounded-md border border-border-subtle bg-surface" style={{ width: (OVERVIEW_ROW_HEIGHT - 24) * 0.67 }}>
                    {series.poster_url ? (
                      <img src={remoteImage(series.poster_url, "small")} alt="" loading="lazy" className="size-full object-cover" />
                    ) : (
                      <span className="flex size-full items-center justify-center text-ink-muted">
                        <Tv aria-hidden className="size-6" />
                      </span>
                    )}
                  </div>
                  <div className="flex min-w-0 flex-1 flex-col gap-1">
                    <div className="flex items-center gap-2">
                      <MonitorBookmark series={series} categoryId={categoryId} />
                      <span className="truncate text-base font-medium">{series.title}</span>
                    </div>
                    {series.overview && <p className="line-clamp-3 text-sm text-ink-muted">{series.overview}</p>}
                    <div className="mt-auto flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-ink-muted">
                      <span className={`size-2 rounded-full ${progressTone(series).fill}`} />
                      {details.map((detail) => (
                        <span key={String(detail)}>{detail}</span>
                      ))}
                    </div>
                  </div>
                </div>
              </Link>
            </div>
          );
        })}
      </div>
    </div>
  );
}

/** A TV library's Series level, built from the same parts as the music Albums level: one list core with the URL as its view
 *  state, poster, overview and table views, the poster options, and a selection with its bar. */
export function SeriesIndex() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const library = useLibrary(categoryId);
  const queryClient = useQueryClient();
  const view = useRouteView(FILTER_KEYS);
  const [storedMode, setMode] = usePreference<IndexView>(`series.view.${categoryId}`, "poster");
  const mode: IndexView = storedMode === "list" || storedMode === "overview" ? storedMode : "poster";
  const [storedOptions, setOptions] = usePreference<PosterOptions>("posters.options", DEFAULT_POSTER_OPTIONS);
  const options = { ...DEFAULT_POSTER_OPTIONS, ...storedOptions };
  const frameRef = useRef<HTMLDivElement>(null);
  const narrow = useNarrow();
  const metrics = useGridMetrics(frameRef, POSTER_WIDTHS[options.size], POSTER_GAP, POSTER_PADDING);
  const isPoster = mode === "poster";
  const profileNames = useQuery({
    queryKey: ["qualityprofiles"],
    queryFn: async () => {
      const { data, error } = await api.GET("/qualityprofiles");
      if (error) throw error;
      return data;
    },
  });
  const everything = useQuery({ queryKey: ["series", categoryId, "all"], queryFn: () => fetchAll(categoryId), staleTime: 15_000 });

  const list = useLibraryList<Series>(
    {
      // A new read of the library renames the list, which reads it again.
      name: `series-index-${categoryId}-${everything.dataUpdatedAt}`,
      idOf: (series) => series.id,
      rowHeight: isPoster ? posterRowHeight(options, metrics.cellWidth, POSTER_RATIO) : mode === "overview" ? OVERVIEW_ROW_HEIGHT : narrow ? 120 : 40,
      itemsPerRow: isPoster ? metrics.columns : 1,
      fetchPage: async ({ query }) => {
        const found = matching(await loadAll(queryClient, categoryId), query.filters);
        const key = (query.sort && query.sort in SORTS ? query.sort : "title") as SortKey;
        found.sort(SORTS[key].compare);
        if (query.direction === "desc") found.reverse();
        return { items: found };
      },
    },
    view,
  );

  const choices = useMemo(() => {
    const count = (values: (string | undefined)[]) => {
      const tally = new Map<string, number>();
      for (const value of values) if (value) tally.set(value, (tally.get(value) ?? 0) + 1);
      return [...tally.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([value]) => value);
    };
    const all = everything.data ?? [];
    return {
      networks: count(all.map((series) => series.network)),
      genres: count(all.flatMap((series) => series.genres ?? [])),
      profiles: [...new Set(all.map((series) => series.quality_profile_id).filter((id): id is number => id != null))],
    };
  }, [everything.data]);

  const exclusions = useQuery({
    queryKey: ["series-exclusions"],
    retry: false,
    queryFn: async () => {
      const { data, error } = await api.GET("/series/exclusions");
      if (error) throw error;
      return data;
    },
  });
  const allow = useMutation({
    mutationFn: async (entry: { provider: string; external_id: string }) => {
      const { error } = await api.DELETE("/series/exclusions", { params: { query: entry } });
      if (error) throw error;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["series-exclusions"] }),
  });

  const setFilter = (key: string, value: string) => view.setQuery({ ...view.query, filters: { ...view.query.filters, [key]: value } });
  const filters = view.query.filters;
  const manage = library?.permission === "manage";
  const selectedIds = list.items.filter((series) => list.isSelected(series.id)).map((series) => series.id);

  const quiet = "rounded-md border border-border-subtle px-2 py-1 text-ink-muted enabled:hover:text-ink disabled:opacity-50";
  const control = "rounded-md border border-border-subtle bg-surface px-1.5 py-1 text-ink";
  const columns: readonly Column<Series>[] = [
    { key: "title", header: "Title", sortable: true, width: "minmax(12rem,2fr)", render: (series) => <Link to="/library/$categoryId/series/$seriesId" params={{ categoryId, seriesId: String(series.id) }} className="hover:underline" onClick={(event) => event.stopPropagation()}>{series.title}</Link> },
    { key: "year", header: "Year", sortable: true, width: "4.5rem", render: (series) => series.year ?? "" },
    { key: "network", header: "Network", sortable: true, width: "minmax(7rem,1fr)", render: (series) => series.network ?? "" },
    { key: "status", header: "Status", width: "6rem", render: (series) => (series.status === "ended" ? "Ended" : "Continuing") },
    { key: "seasons", header: "Seasons", width: "5rem", render: (series) => (series.season_states ?? []).filter((season) => season.number > 0).length },
    { key: "progress", header: "Episodes", sortable: true, width: "8rem", render: (series) => `${series.counts.present}/${series.counts.monitored}` },
    { key: "profile", header: "Quality profile", width: "minmax(7rem,1fr)", render: (series) => (profileNames.data ?? []).find((entry) => entry.id === series.quality_profile_id)?.name ?? "" },
    { key: "monitored", header: "Monitored", width: "6rem", render: (series) => <MonitorBookmark series={series} categoryId={categoryId} /> },
    { key: "added", header: "Added", sortable: true, width: "6.5rem", render: (series) => new Date(series.added_at * 1000).toLocaleDateString() },
  ];

  return (
    <div ref={frameRef} className="flex h-full flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border-subtle px-4 py-2">
        <h2 className="text-sm font-medium">{library?.name ?? "Series"}</h2>
        <span className="text-xs text-ink-muted">
          {list.items.length === (everything.data?.length ?? 0) ? `${list.items.length} series` : `${list.items.length} of ${everything.data?.length ?? 0}`}
        </span>
        <div className="ml-auto flex flex-wrap items-center gap-2 text-xs">
          <label className="relative flex items-center">
            <Search aria-hidden className="pointer-events-none absolute left-2 size-3.5 text-ink-muted" />
            <input
              defaultValue={filters.q ?? ""}
              onChange={(event) => setFilter("q", event.target.value)}
              placeholder="Filter series"
              aria-label="Filter series"
              className={`${control} w-44 pl-7`}
            />
          </label>
          <label className="flex items-center gap-1 text-ink-muted">
            Status
            <select value={filters.status ?? ""} onChange={(event) => setFilter("status", event.target.value)} className={control}>
              <option value="">All</option>
              <option value="continuing">Continuing</option>
              <option value="ended">Ended</option>
            </select>
          </label>
          {choices.networks.length > 1 && (
            <label className="flex items-center gap-1 text-ink-muted">
              Network
              <select value={filters.network ?? ""} onChange={(event) => setFilter("network", event.target.value)} className={`${control} max-w-40`}>
                <option value="">All</option>
                {choices.networks.map((name) => (
                  <option key={name} value={name}>
                    {name}
                  </option>
                ))}
              </select>
            </label>
          )}
          {choices.genres.length > 1 && (
            <label className="flex items-center gap-1 text-ink-muted">
              Genre
              <select value={filters.genre ?? ""} onChange={(event) => setFilter("genre", event.target.value)} className={control}>
                <option value="">All</option>
                {choices.genres.map((name) => (
                  <option key={name} value={name}>
                    {name}
                  </option>
                ))}
              </select>
            </label>
          )}
          {choices.profiles.length > 1 && (
            <label className="flex items-center gap-1 text-ink-muted">
              Quality
              <select value={filters.profile ?? ""} onChange={(event) => setFilter("profile", event.target.value)} className={control}>
                <option value="">All</option>
                {choices.profiles.map((id) => (
                  <option key={id} value={id}>
                    {(profileNames.data ?? []).find((entry) => entry.id === id)?.name ?? `Profile ${id}`}
                  </option>
                ))}
              </select>
            </label>
          )}
          <label className="flex items-center gap-1 text-ink-muted">
            <input type="checkbox" checked={filters.monitored === "1"} onChange={(event) => setFilter("monitored", event.target.checked ? "1" : "")} /> Monitored
          </label>
          <label className="flex items-center gap-1 text-ink-muted">
            <input type="checkbox" checked={filters.missing === "1"} onChange={(event) => setFilter("missing", event.target.checked ? "1" : "")} /> Missing episodes
          </label>
          <CustomFilterMenu fields={SERIES_FILTER_FIELDS} storageKey={`series.${categoryId}`} where={filters.where ?? ""} onChange={(where) => setFilter("where", where)} />
          <label className="flex items-center gap-1 text-ink-muted">
            Sort
            <select
              value={view.query.sort ?? "title"}
              onChange={(event) => view.setQuery({ ...view.query, sort: event.target.value })}
              className={control}
            >
              {(Object.keys(SORTS) as SortKey[]).map((key) => (
                <option key={key} value={key}>
                  {SORTS[key].label}
                </option>
              ))}
            </select>
          </label>
          {manage && !list.selectionIsEmpty && (
            <>
              <span className="text-ink-muted">{selectedIds.length} selected</span>
              <button type="button" onClick={list.clearSelection} className={quiet}>
                Clear
              </button>
            </>
          )}
          {manage && (
            <button type="button" onClick={list.selectAllMatching} className={quiet}>
              Select all matching
            </button>
          )}
          <IndexViewToggle view={mode} onChange={setMode} />
          {isPoster && <PosterOptionsMenu options={options} onChange={setOptions} />}
          <Link to="/library/$categoryId/discover" params={{ categoryId }} className="ml-1 flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink">
            <Plus aria-hidden className="size-4" />
            Add new
          </Link>
        </div>
      </div>
      {manage && selectedIds.length > 0 && (
        <SeriesBulkBar
          categoryId={categoryId}
          ids={selectedIds}
          profiles={profileNames.data ?? []}
          onDone={() => {
            list.clearSelection();
            void queryClient.invalidateQueries({ queryKey: ["series", categoryId, "all"] });
                }}
        />
      )}
      {(exclusions.data ?? []).length > 0 && (
        <details className="border-b border-border-subtle px-4 py-1.5 text-xs text-ink-muted">
          <summary className="cursor-pointer">Deleted series that are not added back ({exclusions.data?.length})</summary>
          <ul className="mt-1 flex flex-col gap-1">
            {(exclusions.data ?? []).map((entry) => (
              <li key={`${entry.provider}:${entry.external_id}`} className="flex items-center gap-2">
                <span className="text-ink">{entry.title}</span>
                <button type="button" disabled={allow.isPending} onClick={() => allow.mutate(entry)} className="rounded border border-border-subtle px-1.5 py-0.5 hover:text-ink">
                  Allow again
                </button>
              </li>
            ))}
          </ul>
        </details>
      )}
      <div className="min-h-0 flex-1">
        {list.isLoading && <p className="p-4 text-sm text-ink-muted">Loading the library…</p>}
        {list.error !== null && <p className="p-4 text-sm text-danger">The series could not be read.</p>}
        {!list.isLoading && list.error === null && list.items.length === 0 && (
          <div className="mx-auto mt-16 flex max-w-md flex-col items-center gap-3 text-center">
            <Tv aria-hidden className="size-10 text-ink-muted" />
            <p className="text-sm">{(everything.data?.length ?? 0) === 0 ? "No series in this library yet." : "No series match these filters."}</p>
            {(everything.data?.length ?? 0) === 0 && (
              <Link to="/library/$categoryId/discover" params={{ categoryId }} className="rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink">
                Add your first series
              </Link>
            )}
          </div>
        )}
        {list.items.length > 0 &&
          (mode === "list" ? (
            <ListTable list={list} columns={columns} idOf={(series) => series.id} />
          ) : mode === "overview" ? (
            <SeriesOverviewList list={list} categoryId={categoryId} />
          ) : (
            <PosterGrid list={list} idOf={(series) => series.id} renderPoster={(series) => <SeriesPosterTile series={series} categoryId={categoryId} options={options} />} />
          ))}
      </div>
    </div>
  );
}

type BulkAction =
  | { kind: "monitor"; option: string }
  | { kind: "profile"; profileId: number | null }
  | { kind: "delete"; files: boolean; exclude: boolean };

const MONITOR_OPTIONS: [string, string][] = [
  ["all", "Every episode"],
  ["future", "Episodes that have not aired yet"],
  ["missing", "Missing episodes"],
  ["existing", "Episodes on disk"],
  ["first_season", "The first season"],
  ["last_season", "The last season"],
  ["none", "Nothing"],
];

/** Sonarr's series editor: act on every ticked series after a confirmation that says what will happen. */
function SeriesBulkBar({ categoryId, ids, profiles, onDone }: { categoryId: string; ids: number[]; profiles: { id: number; name: string }[]; onDone: () => void }) {
  const [option, setOption] = useState("existing");
  const [profile, setProfile] = useState("");
  const [files, setFiles] = useState(false);
  const [exclude, setExclude] = useState(true);
  const [pending, setPending] = useState<BulkAction | null>(null);
  const [progress, setProgress] = useState<{ done: number; failed: number } | null>(null);

  const run = useMutation({
    mutationFn: async (action: BulkAction) => {
      const query = { category_id: Number(categoryId) };
      let done = 0;
      let failed = 0;
      setProgress({ done, failed });
      for (const id of ids) {
        let error: unknown;
        if (action.kind === "monitor") {
          ({ error } = await api.PUT("/series/{id}/monitoring", { params: { path: { id }, query }, body: { option: action.option as never } }));
        } else if (action.kind === "profile") {
          ({ error } = await api.PUT("/series/{id}/quality", { params: { path: { id }, query }, body: { quality_profile_id: action.profileId } }));
        } else {
          ({ error } = await api.DELETE("/series/{id}", { params: { path: { id }, query: { ...query, delete_files: action.files, exclude: action.exclude } } }));
        }
        if (error) failed++;
        else done++;
        setProgress({ done, failed });
      }
    },
    onSuccess: () => {
      setPending(null);
      onDone();
    },
  });

  const control = "rounded-md border border-border-subtle bg-canvas px-2 py-1 text-xs text-ink";
  const button = "rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted enabled:hover:text-ink disabled:opacity-50";
  const description =
    pending?.kind === "monitor"
      ? `Monitor ${MONITOR_OPTIONS.find(([key]) => key === pending.option)?.[1].toLowerCase()} of ${ids.length} series. Episodes already chosen by hand are re-decided.`
      : pending?.kind === "profile"
        ? `${ids.length} series ${pending.profileId === null ? "go back to the library's quality profiles" : `get the quality profile ${profiles.find((entry) => entry.id === pending.profileId)?.name}`}.`
        : pending?.kind === "delete"
          ? `${ids.length} series are removed from Solarr. ${pending.files ? "Their files move to the recycle bin." : "Their files stay where they are."} ${pending.exclude ? "A scan will not add them back." : "A scan may add them back."}`
          : "";

  return (
    <div className="flex flex-wrap items-center gap-3 border-b border-border-subtle bg-surface px-4 py-2 text-xs">
      <span className="font-medium">{ids.length} selected</span>
      <label className="flex items-center gap-1">
        Monitor
        <select value={option} onChange={(event) => setOption(event.target.value)} className={control}>
          {MONITOR_OPTIONS.map(([key, label]) => (
            <option key={key} value={key}>
              {label}
            </option>
          ))}
        </select>
        <button type="button" disabled={ids.length === 0} onClick={() => setPending({ kind: "monitor", option })} className={button}>
          Apply
        </button>
      </label>
      <label className="flex items-center gap-1">
        Quality profile
        <select value={profile} onChange={(event) => setProfile(event.target.value)} className={control}>
          <option value="">The library's own</option>
          {profiles.map((entry) => (
            <option key={entry.id} value={entry.id}>
              {entry.name}
            </option>
          ))}
        </select>
        <button type="button" disabled={ids.length === 0} onClick={() => setPending({ kind: "profile", profileId: profile === "" ? null : Number(profile) })} className={button}>
          Apply
        </button>
      </label>
      <span className="flex items-center gap-2">
        <label className="flex items-center gap-1">
          <input type="checkbox" checked={files} onChange={(event) => setFiles(event.target.checked)} /> files to the recycle bin
        </label>
        <label className="flex items-center gap-1">
          <input type="checkbox" checked={exclude} onChange={(event) => setExclude(event.target.checked)} /> do not add back
        </label>
        <button type="button" disabled={ids.length === 0} onClick={() => setPending({ kind: "delete", files, exclude })} className={`${button} text-danger`}>
          Delete…
        </button>
      </span>
      <Dialog.Root open={pending !== null} onOpenChange={(open) => !open && !run.isPending && setPending(null)}>
        <Dialog.Portal>
          <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40" />
          <Dialog.Content className="fixed left-1/2 top-1/2 z-50 flex w-[min(30rem,92vw)] -translate-x-1/2 -translate-y-1/2 flex-col gap-3 rounded-lg border border-border-subtle bg-canvas p-4">
            <Dialog.Title className="text-sm font-medium">Change {ids.length} series?</Dialog.Title>
            <Dialog.Description className="text-xs text-ink-muted">{description}</Dialog.Description>
            {progress && run.isPending && (
              <p className="text-xs text-ink-muted">
                {progress.done + progress.failed} of {ids.length} done{progress.failed > 0 ? `, ${progress.failed} failed` : ""}.
              </p>
            )}
            {run.isError && <p className="text-xs text-danger">That could not be done.</p>}
            <div className="flex justify-end gap-2">
              <Dialog.Close disabled={run.isPending} className={button}>
                Cancel
              </Dialog.Close>
              <button type="button" disabled={run.isPending || pending === null} onClick={() => pending && run.mutate(pending)} className="rounded-md bg-accent px-3 py-1 text-xs text-accent-ink disabled:opacity-50">
                {run.isPending ? "Working…" : "Yes, do it"}
              </button>
            </div>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </div>
  );
}
