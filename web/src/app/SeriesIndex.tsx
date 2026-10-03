import { useMemo, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Plus, Search, Tv } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { useLibrary } from "@/lib/useLibrary";
import { usePreference } from "@/lib/usePreference";

type Series = components["schemas"]["SeriesSummary"];

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
      style={{ contentVisibility: "auto", containIntrinsicSize: "auto 320px" }}
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
  const shown = useMemo(() => {
    const needle = search.trim().toLowerCase();
    return all
      .filter((series) => needle === "" || series.title.toLowerCase().includes(needle))
      .filter((series) => status === "all" || series.status === status)
      .filter((series) => !onlyMissing || series.counts.aired_missing > 0)
      .filter((series) => !onlyMonitored || series.monitored)
      .sort(SORTS[sort].compare);
  }, [all, search, status, onlyMissing, onlyMonitored, sort]);

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
      <div className="min-h-0 flex-1 overflow-auto p-4">
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
        <div className="grid gap-4" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(150px, 1fr))" }}>
          {shown.map((series) => (
            <SeriesTile key={series.id} series={series} categoryId={categoryId} />
          ))}
        </div>
      </div>
    </div>
  );
}
