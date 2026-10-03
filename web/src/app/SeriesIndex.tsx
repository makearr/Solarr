import { useMemo, useRef, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import { Plus, Search, Tv } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { useLibrary } from "@/lib/useLibrary";
import { usePreference } from "@/lib/usePreference";
import { useGridMetrics } from "@/list/useGridMetrics";

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
          <img src={series.poster_url} alt="" loading="lazy" className="size-full object-cover" referrerPolicy="no-referrer" />
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

/** A TV library's Series level: every series as a poster, with Sonarr's filters and sorts applied in the browser. */
export function SeriesIndex() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const library = useLibrary(categoryId);
  const [search, setSearch] = useState("");
  const [sort, setSort] = usePreference<SortKey>(`series.sort.${categoryId}`, "title");
  const [status, setStatus] = useState<"all" | "continuing" | "ended">("all");
  const [onlyMissing, setOnlyMissing] = useState(false);
  const [onlyMonitored, setOnlyMonitored] = useState(false);
  const [network, setNetwork] = useState("");
  const [genre, setGenre] = useState("");
  const [profile, setProfile] = useState(0);

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
  const rowCount = Math.ceil(shown.length / metrics.columns);
  const virtualizer = useVirtualizer({
    count: rowCount,
    getScrollElement: () => scroller.current,
    estimateSize: () => metrics.cellWidth * 1.5 + TILE_CAPTION + POSTER_GAP,
    overscan: 3,
  });

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
        <Link
          to="/library/$categoryId/discover"
          params={{ categoryId }}
          className="flex items-center gap-1 rounded-md bg-accent px-2.5 py-1 text-xs font-medium text-accent-ink"
        >
          <Plus aria-hidden className="size-3.5" /> Add series
        </Link>
      </div>
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
        <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
          {virtualizer.getVirtualItems().map((row) => (
            <div
              key={row.key}
              className="absolute inset-x-0 grid"
              style={{ height: row.size, transform: `translateY(${row.start}px)`, gap: POSTER_GAP, gridTemplateColumns: `repeat(${metrics.columns}, minmax(0, 1fr))` }}
            >
              {shown.slice(row.index * metrics.columns, (row.index + 1) * metrics.columns).map((series) => (
                <SeriesTile key={series.id} series={series} categoryId={categoryId} />
              ))}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
