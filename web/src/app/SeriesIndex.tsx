import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import * as Dialog from "@radix-ui/react-dialog";
import { CheckSquare, LayoutGrid, List, Plus, Rows3, Search, Tv } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { useLibrary } from "@/lib/useLibrary";
import { usePreference } from "@/lib/usePreference";
import { useGridMetrics } from "@/list/useGridMetrics";
import { remoteImage } from "@/lib/remoteImage";

type Series = components["schemas"]["SeriesSummary"];

const POSTER_MIN_WIDTH = 150;
const POSTER_GAP = 16;
const POSTER_PADDING = 16;
// The progress bar, two lines of caption and the gaps between them under a poster.
const TILE_CAPTION = 70;

type SortKey = "title" | "year" | "added" | "network" | "next" | "progress";

const SORTS: Record<SortKey, { label: string; compare: (a: Series, b: Series) => number }> = {
  title: { label: "Title", compare: (a, b) => a.sort_title.localeCompare(b.sort_title) },
  year: { label: "Year", compare: (a, b) => (b.year ?? 0) - (a.year ?? 0) },
  added: { label: "Recently added", compare: (a, b) => b.added_at - a.added_at },
  network: { label: "Network", compare: (a, b) => (a.network ?? "~").localeCompare(b.network ?? "~") },
  next: {
    label: "Next airing",
    compare: (a, b) => (a.next_airing ?? "9999").localeCompare(b.next_airing ?? "9999"),
  },
  progress: { label: "Episodes missing", compare: (a, b) => b.counts.aired_missing - a.counts.aired_missing },
};

/** Sonarr's bar under a poster: green when everything wanted is here, red when aired episodes are missing. */
export function progressTone(series: Pick<Series, "counts" | "monitored">): { fill: string; label: string } {
  const { monitored, present, aired_missing: missing } = series.counts;
  if (!series.monitored || monitored === 0) return { fill: "bg-ink-muted", label: "Unmonitored" };
  if (missing > 0 && present === 0) return { fill: "bg-danger", label: "Missing" };
  if (missing > 0) return { fill: "bg-warn", label: "Partial" };
  if (present >= monitored) return { fill: "bg-ok", label: "Complete" };
  return { fill: "bg-accent", label: "Waiting for air dates" };
}

export function SeriesTile({ series, categoryId }: { series: Series; categoryId: string }) {
  const tone = progressTone(series);
  const percent = series.counts.monitored > 0 ? Math.min(100, (100 * series.counts.present) / series.counts.monitored) : 0;
  return (
    <Link
      to="/library/$categoryId/series/$seriesId"
      params={{ categoryId, seriesId: String(series.id) }}
      className="group flex flex-col gap-1.5 rounded-lg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
    >
      <span className="relative block aspect-[2/3] overflow-hidden rounded-lg border border-border-subtle bg-surface">
        {series.poster_url ? (
          <img src={remoteImage(series.poster_url, "small")} alt="" loading="lazy" className="size-full object-cover" />
        ) : (
          <span className="flex size-full items-center justify-center text-ink-muted">
            <Tv aria-hidden className="size-10" />
          </span>
        )}
        {series.status === "ended" && (
          <span className="absolute right-1.5 top-1.5 rounded bg-canvas/85 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-ink-muted">
            Ended
          </span>
        )}
      </span>
      <span className="block h-1.5 overflow-hidden rounded-full bg-border-subtle" title={`${tone.label}: ${series.counts.present} of ${series.counts.monitored}`}>
        <span className={`block h-full ${tone.fill}`} style={{ width: `${percent}%` }} />
      </span>
      <span className="truncate text-sm font-medium group-hover:underline">{series.title}</span>
      <span className="truncate text-xs text-ink-muted">
        {[series.year, series.network, `${series.counts.present}/${series.counts.monitored}`].filter(Boolean).join(" · ")}
      </span>
    </Link>
  );
}

type ViewKey = "posters" | "overview" | "table";

const VIEWS: { key: ViewKey; label: string; icon: typeof LayoutGrid }[] = [
  { key: "posters", label: "Posters", icon: LayoutGrid },
  { key: "overview", label: "Overview", icon: Rows3 },
  { key: "table", label: "Table", icon: List },
];

const OVERVIEW_ROW = 132;
const TABLE_ROW = 40;

function ProgressBar({ series }: { series: Series }) {
  const tone = progressTone(series);
  const percent = series.counts.monitored > 0 ? Math.min(100, (100 * series.counts.present) / series.counts.monitored) : 0;
  return (
    <span className="block h-1.5 w-full overflow-hidden rounded-full bg-border-subtle" title={`${tone.label}: ${series.counts.present} of ${series.counts.monitored}`}>
      <span className={`block h-full ${tone.fill}`} style={{ width: `${percent}%` }} />
    </span>
  );
}

/** Sonarr's overview: a small poster beside the title, facts and the start of the plot. */
function OverviewRow({ series, categoryId }: { series: Series; categoryId: string }) {
  return (
    <Link
      to="/library/$categoryId/series/$seriesId"
      params={{ categoryId, seriesId: String(series.id) }}
      className="flex h-full min-h-0 gap-3 overflow-hidden rounded-lg border border-border-subtle p-2 hover:border-accent focus-visible:outline-2 focus-visible:outline-accent"
    >
      <span className="block aspect-[2/3] h-full max-h-full shrink-0 overflow-hidden rounded bg-surface">
        {series.poster_url ? (
          <img src={remoteImage(series.poster_url, "thumb")} alt="" loading="lazy" className="size-full object-cover" />
        ) : (
          <span className="flex size-full items-center justify-center text-ink-muted">
            <Tv aria-hidden className="size-6" />
          </span>
        )}
      </span>
      <span className="flex min-w-0 flex-1 flex-col gap-1">
        <span className="flex items-baseline gap-2">
          <span className="truncate text-sm font-semibold">{series.title}</span>
          <span className="shrink-0 text-xs text-ink-muted">
            {[series.year, series.network, series.status === "ended" ? "Ended" : "Continuing"].filter(Boolean).join(" · ")}
          </span>
        </span>
        <span className="line-clamp-3 text-xs text-ink-muted">{series.overview || "No overview."}</span>
        <span className="mt-auto flex items-center gap-3 text-xs text-ink-muted">
          <span className="w-40">
            <ProgressBar series={series} />
          </span>
          <span>
            {series.counts.present}/{series.counts.monitored} episodes
          </span>
          {(series.genres ?? []).length > 0 && <span className="truncate">{(series.genres ?? []).slice(0, 3).join(", ")}</span>}
        </span>
      </span>
    </Link>
  );
}

const TABLE_COLUMNS: { header: string; sort?: SortKey; width: string; cell: (series: Series, profile: string) => React.ReactNode }[] = [
  { header: "Title", sort: "title", width: "minmax(12rem,2fr)", cell: (series) => <span className="truncate font-medium">{series.title}</span> },
  { header: "Year", sort: "year", width: "4.5rem", cell: (series) => series.year ?? "" },
  { header: "Network", sort: "network", width: "minmax(7rem,1fr)", cell: (series) => <span className="truncate">{series.network ?? ""}</span> },
  { header: "Status", width: "6rem", cell: (series) => (series.status === "ended" ? "Ended" : "Continuing") },
  { header: "Seasons", width: "5rem", cell: (series) => (series.season_states ?? []).filter((season) => season.number > 0).length },
  { header: "Episodes", sort: "progress", width: "minmax(8rem,1fr)", cell: (series) => (
    <span className="flex w-full items-center gap-2">
      <span className="w-16 shrink-0">
        <ProgressBar series={series} />
      </span>
      {series.counts.present}/{series.counts.monitored}
    </span>
  ) },
  { header: "Quality profile", width: "minmax(7rem,1fr)", cell: (_series, profile) => <span className="truncate">{profile}</span> },
  { header: "Monitored", width: "5.5rem", cell: (series) => (series.monitored ? "Yes" : "No") },
  { header: "Added", sort: "added", width: "6.5rem", cell: (series) => new Date(series.added_at * 1000).toLocaleDateString() },
];

const TABLE_GRID = TABLE_COLUMNS.map((column) => column.width).join(" ");
const TABLE_GRID_SELECTING = `2rem ${TABLE_GRID}`;

/** A TV library's Series level: every series as a poster, with Sonarr's filters and sorts applied in the browser. */
export function SeriesIndex() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const library = useLibrary(categoryId);
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [sort, setSort] = usePreference<SortKey>(`series.sort.${categoryId}`, "title");
  const [status, setStatus] = useState<"all" | "continuing" | "ended">("all");
  const [onlyMissing, setOnlyMissing] = useState(false);
  const [onlyMonitored, setOnlyMonitored] = useState(false);
  const [network, setNetwork] = useState("");
  const [genre, setGenre] = useState("");
  const [profile, setProfile] = useState(0);
  const [view, setView] = usePreference<ViewKey>(`series.view.${categoryId}`, "posters");
  const [selecting, setSelecting] = useState(false);
  const [selected, setSelected] = useState<Set<number>>(new Set());

  const pages = useInfiniteQuery({
    queryKey: ["series", categoryId],
    initialPageParam: "",
    queryFn: async ({ pageParam }) => {
      const { data, error } = await api.GET("/series", {
        params: { query: { category_id: Number(categoryId), limit: 500, ...(pageParam ? { cursor: pageParam } : {}) } },
      });
      if (error) throw error;
      return data;
    },
    getNextPageParam: (last) => last.next_cursor || undefined,
  });
  // A library of a thousand series is two requests; read them all so filters and sorts see everything.
  if (pages.hasNextPage && !pages.isFetchingNextPage) void pages.fetchNextPage();

  const all = useMemo(() => (pages.data?.pages ?? []).flatMap((page) => page.items), [pages.data]);
  // The choices each filter offers are what this library actually holds, most common first.
  const choices = useMemo(() => {
    const count = (values: (string | undefined)[]) => {
      const tally = new Map<string, number>();
      for (const value of values) if (value) tally.set(value, (tally.get(value) ?? 0) + 1);
      return [...tally.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([value]) => value);
    };
    return {
      networks: count(all.map((series) => series.network)),
      genres: count(all.flatMap((series) => series.genres ?? [])),
      profiles: [...new Set(all.map((series) => series.quality_profile_id).filter((id): id is number => id != null))],
    };
  }, [all]);
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
  const profileNames = useQuery({
    queryKey: ["qualityprofiles"],
    queryFn: async () => {
      const { data, error } = await api.GET("/qualityprofiles");
      if (error) throw error;
      return data;
    },
  });
  const shown = useMemo(() => {
    const needle = search.trim().toLowerCase();
    return all
      .filter((series) => needle === "" || series.title.toLowerCase().includes(needle))
      .filter((series) => network === "" || series.network === network)
      .filter((series) => genre === "" || (series.genres ?? []).includes(genre))
      .filter((series) => profile === 0 || series.quality_profile_id === profile)
      .filter((series) => status === "all" || series.status === status)
      .filter((series) => !onlyMissing || series.counts.aired_missing > 0)
      .filter((series) => !onlyMonitored || series.monitored)
      .sort(SORTS[sort].compare);
  }, [all, search, network, genre, profile, status, onlyMissing, onlyMonitored, sort]);

  // The wall is virtualised a row of posters at a time: a library of a thousand series must not mount a thousand images.
  const scroller = useRef<HTMLDivElement>(null);
  const metrics = useGridMetrics(scroller, POSTER_MIN_WIDTH, POSTER_GAP, POSTER_PADDING);
  const columns = view === "posters" ? metrics.columns : 1;
  const rowCount = Math.ceil(shown.length / columns);
  const rowHeight = view === "posters" ? metrics.cellWidth * 1.5 + TILE_CAPTION + POSTER_GAP : view === "overview" ? OVERVIEW_ROW + 8 : TABLE_ROW;
  const virtualizer = useVirtualizer({
    count: rowCount,
    getScrollElement: () => scroller.current,
    estimateSize: () => rowHeight,
    overscan: 3,
  });
  useEffect(() => virtualizer.measure(), [virtualizer, view, rowHeight]);

  const control = "rounded-md border border-border-subtle bg-canvas px-2 py-1 text-xs text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
  return (
    <div className="flex h-full flex-col">
      <div className="flex flex-wrap items-center gap-3 border-b border-border-subtle px-4 py-2">
        <h1 className="text-base font-semibold">{library?.name ?? "Series"}</h1>
        <span className="text-xs text-ink-muted">{shown.length === all.length ? `${all.length} series` : `${shown.length} of ${all.length}`}</span>
        <label className="relative ml-auto flex items-center">
          <Search aria-hidden className="pointer-events-none absolute left-2 size-3.5 text-ink-muted" />
          <input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Filter series"
            aria-label="Filter series"
            className={`${control} w-48 pl-7`}
          />
        </label>
        <span role="group" aria-label="View" className="flex overflow-hidden rounded-md border border-border-subtle">
          {VIEWS.map(({ key, label, icon: Icon }) => (
            <button
              key={key}
              type="button"
              aria-pressed={view === key}
              title={label}
              onClick={() => setView(key)}
              className={`flex items-center gap-1 px-2 py-1 text-xs ${view === key ? "bg-accent/10 text-ink" : "text-ink-muted hover:text-ink"}`}
            >
              <Icon aria-hidden className="size-3.5" />
              <span className="hidden sm:inline">{label}</span>
            </button>
          ))}
        </span>
        <label className="flex items-center gap-1 text-xs text-ink-muted">
          Sort
          <select value={sort} onChange={(event) => setSort(event.target.value as SortKey)} className={control}>
            {(Object.keys(SORTS) as SortKey[]).map((key) => (
              <option key={key} value={key}>
                {SORTS[key].label}
              </option>
            ))}
          </select>
        </label>
        <label className="flex items-center gap-1 text-xs text-ink-muted">
          Status
          <select value={status} onChange={(event) => setStatus(event.target.value as typeof status)} className={control}>
            <option value="all">All</option>
            <option value="continuing">Continuing</option>
            <option value="ended">Ended</option>
          </select>
        </label>
        {choices.networks.length > 1 && (
          <label className="flex items-center gap-1 text-xs text-ink-muted">
            Network
            <select value={network} onChange={(event) => setNetwork(event.target.value)} className={`${control} max-w-40`}>
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
          <label className="flex items-center gap-1 text-xs text-ink-muted">
            Genre
            <select value={genre} onChange={(event) => setGenre(event.target.value)} className={control}>
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
          <label className="flex items-center gap-1 text-xs text-ink-muted">
            Quality
            <select value={profile} onChange={(event) => setProfile(Number(event.target.value))} className={control}>
              <option value={0}>All</option>
              {choices.profiles.map((id) => (
                <option key={id} value={id}>
                  {(profileNames.data ?? []).find((entry) => entry.id === id)?.name ?? `Profile ${id}`}
                </option>
              ))}
            </select>
          </label>
        )}
        <label className="flex items-center gap-1 text-xs text-ink-muted">
          <input type="checkbox" checked={onlyMonitored} onChange={(event) => setOnlyMonitored(event.target.checked)} /> Monitored
        </label>
        <label className="flex items-center gap-1 text-xs text-ink-muted">
          <input type="checkbox" checked={onlyMissing} onChange={(event) => setOnlyMissing(event.target.checked)} /> Missing episodes
        </label>
        {library?.permission === "manage" && (
          <button
            type="button"
            aria-pressed={selecting}
            onClick={() => {
              setSelecting(!selecting);
              setSelected(new Set());
              if (!selecting) setView("table");
            }}
            className={`flex items-center gap-1 rounded-md border px-2 py-1 text-xs ${selecting ? "border-accent bg-accent/10 text-ink" : "border-border-subtle text-ink-muted hover:text-ink"}`}
          >
            <CheckSquare aria-hidden className="size-3.5" /> Edit several
          </button>
        )}
        <Link
          to="/library/$categoryId/discover"
          params={{ categoryId }}
          className="flex items-center gap-1 rounded-md bg-accent px-2.5 py-1 text-xs font-medium text-accent-ink"
        >
          <Plus aria-hidden className="size-3.5" /> Add series
        </Link>
      </div>
      {selecting && (
        <SeriesBulkBar
          categoryId={categoryId}
          ids={[...selected]}
          profiles={profileNames.data ?? []}
          onDone={() => {
            setSelected(new Set());
            void queryClient.invalidateQueries({ queryKey: ["series", categoryId] });
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
      <div ref={scroller} className="min-h-0 flex-1 overflow-auto" style={{ padding: POSTER_PADDING }}>
        {pages.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
        {pages.error !== null && !pages.isLoading && <p className="text-sm text-danger">The series could not be read.</p>}
        {!pages.isLoading && all.length === 0 && pages.error === null && (
          <div className="mx-auto mt-16 flex max-w-md flex-col items-center gap-3 text-center">
            <Tv aria-hidden className="size-10 text-ink-muted" />
            <p className="text-sm">No series in this library yet.</p>
            <Link to="/library/$categoryId/discover" params={{ categoryId }} className="rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink">
              Add your first series
            </Link>
          </div>
        )}
        {view === "table" && shown.length > 0 && (
          <div
            role="row"
            className="sticky top-0 z-10 grid items-center gap-2 border-b border-border-subtle bg-canvas px-2 py-1.5 text-xs font-medium text-ink-muted"
            style={{ gridTemplateColumns: selecting ? TABLE_GRID_SELECTING : TABLE_GRID }}
          >
            {selecting && (
              <input
                type="checkbox"
                aria-label="Select every series shown"
                checked={shown.length > 0 && shown.every((series) => selected.has(series.id))}
                onChange={(event) => setSelected(event.target.checked ? new Set(shown.map((series) => series.id)) : new Set())}
              />
            )}
            {TABLE_COLUMNS.map((column) =>
              column.sort ? (
                <button key={column.header} type="button" onClick={() => setSort(column.sort as SortKey)} className="text-left hover:text-ink">
                  {column.header}
                  {sort === column.sort ? " ▾" : ""}
                </button>
              ) : (
                <span key={column.header}>{column.header}</span>
              ),
            )}
          </div>
        )}
        <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
          {virtualizer.getVirtualItems().map((row) => {
            const slice = shown.slice(row.index * columns, (row.index + 1) * columns);
            return (
              <div
                key={row.key}
                className="absolute inset-x-0 grid"
                style={{
                  height: row.size,
                  transform: `translateY(${row.start}px)`,
                  gap: view === "posters" ? POSTER_GAP : 0,
                  gridTemplateColumns: view === "posters" ? `repeat(${metrics.columns}, minmax(0, 1fr))` : "minmax(0, 1fr)",
                  gridTemplateRows: "minmax(0, 1fr)",
                }}
              >
                {slice.map((series) =>
                  view === "posters" ? (
                    <SeriesTile key={series.id} series={series} categoryId={categoryId} />
                  ) : view === "overview" ? (
                    <div key={series.id} className="h-full pb-2">
                      <OverviewRow series={series} categoryId={categoryId} />
                    </div>
                  ) : (
                    <div
                      key={series.id}
                      className="grid items-center gap-2 border-b border-border-subtle px-2 text-xs hover:bg-surface"
                      style={{ gridTemplateColumns: selecting ? TABLE_GRID_SELECTING : TABLE_GRID }}
                    >
                      {selecting && (
                        <input
                          type="checkbox"
                          aria-label={`Select ${series.title}`}
                          checked={selected.has(series.id)}
                          onChange={(event) =>
                            setSelected((current) => {
                              const next = new Set(current);
                              if (event.target.checked) next.add(series.id);
                              else next.delete(series.id);
                              return next;
                            })
                          }
                        />
                      )}
                      <Link to="/library/$categoryId/series/$seriesId" params={{ categoryId, seriesId: String(series.id) }} className="contents">
                        {TABLE_COLUMNS.map((column) => (
                          <span key={column.header} className="flex min-w-0 items-center truncate">
                            {column.cell(series, (profileNames.data ?? []).find((entry) => entry.id === series.quality_profile_id)?.name ?? "")}
                          </span>
                        ))}
                      </Link>
                    </div>
                  ),
                )}
              </div>
            );
          })}
        </div>
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
