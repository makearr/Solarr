import { useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Search, Tv } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { usePreference } from "@/lib/usePreference";

import { seasonGroup } from "./SeasonSearch";
import { useDebouncedValue } from "./useDebouncedValue";
import { remoteImage } from "@/lib/remoteImage";

type Candidate = components["schemas"]["SeriesCandidate"];
type Monitor = components["schemas"]["AddSeriesRequest"]["monitor"];

const MONITOR_OPTIONS: { value: NonNullable<Monitor>; label: string; help: string }[] = [
  { value: "all", label: "All episodes", help: "Every episode, aired or not (specials aside)." },
  { value: "future", label: "Future episodes", help: "Only episodes that have not aired yet." },
  { value: "missing", label: "Missing episodes", help: "Every episode that has no file." },
  { value: "existing", label: "Existing episodes", help: "Only episodes you already have files for." },
  { value: "recent", label: "Recent episodes", help: "Aired in the last 90 days, and future ones." },
  { value: "pilot", label: "Pilot episode", help: "Only the first episode." },
  { value: "first_season", label: "First season", help: "Every episode of the first season." },
  { value: "last_season", label: "Last season", help: "Every episode of the latest season." },
  { value: "none", label: "None", help: "Add the series without wanting anything yet." },
];

/** "Start search for missing episodes": one search per monitored season that has aired episodes without a file. */
async function searchEachMonitoredSeason(seriesId: number, categoryId: number) {
  const { data } = await api.GET("/series/{id}", { params: { path: { id: seriesId }, query: { category_id: categoryId } } });
  for (const season of data?.seasons ?? []) {
    if (season.monitored && season.number > 0 && season.counts.aired_missing > 0) {
      await api.POST("/works/{id}/grab", { params: { path: { id: seriesId }, query: { group: seasonGroup(season.number) } } });
    }
  }
}

/** Sonarr's add dialog for one found series: where it goes is the library, what is wanted is the Monitor choice. */
export function AddPanel({ candidate, categoryId, onDone }: { candidate: Candidate; categoryId: string; onDone: () => void }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [monitor, setMonitor] = usePreference<NonNullable<Monitor>>("series.add.monitor", "all");
  const [specials, setSpecials] = useState(false);
  const [profileId, setProfileId] = useState(0);
  const [searchMissing, setSearchMissing] = usePreference<boolean>("series.add.search", false);
  const profiles = useQuery({
    queryKey: ["qualityprofiles"],
    queryFn: async () => {
      const { data, error } = await api.GET("/qualityprofiles");
      if (error) throw error;
      return data;
    },
  });
  const add = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/series", {
        body: {
          category_id: Number(categoryId),
          provider: candidate.provider === "tmdb" ? "tmdb" : "tvdb",
          external_id: candidate.external_id,
          monitor,
          monitor_specials: specials,
          quality_profile_id: profileId || undefined,
        },
      });
      if (error) throw error;
      if (searchMissing) await searchEachMonitoredSeason(data.series_id, Number(categoryId));
      return data;
    },
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: ["series", categoryId] });
      void queryClient.invalidateQueries({ queryKey: ["libraries"] });
      onDone();
      void navigate({ to: "/library/$categoryId/series/$seriesId", params: { categoryId, seriesId: String(result.series_id) } });
    },
  });
  const help = MONITOR_OPTIONS.find((option) => option.value === monitor)?.help;
  return (
    <div className="mt-2 flex flex-col gap-2 rounded-md border border-border-subtle bg-canvas p-3 text-sm">
      <label className="flex flex-wrap items-center gap-2">
        <span className="w-20 text-xs text-ink-muted">Monitor</span>
        <select
          value={monitor}
          onChange={(event) => setMonitor(event.target.value as NonNullable<Monitor>)}
          className="rounded-md border border-border-subtle bg-surface px-2 py-1 text-sm"
        >
          {MONITOR_OPTIONS.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
        <span className="text-xs text-ink-muted">{help}</span>
      </label>
      <label className="flex flex-wrap items-center gap-2">
        <span className="w-20 text-xs text-ink-muted">Quality</span>
        <select
          value={profileId}
          onChange={(event) => setProfileId(Number(event.target.value))}
          className="rounded-md border border-border-subtle bg-surface px-2 py-1 text-sm"
        >
          <option value={0}>The library&apos;s default</option>
          {(profiles.data ?? [])
            .filter((profile) => profile.media_type === "tv")
            .map((profile) => (
              <option key={profile.id} value={profile.id}>
                {profile.name}
              </option>
            ))}
        </select>
      </label>
      <label className="flex items-center gap-2 text-xs text-ink-muted">
        <span className="w-20" />
        <input type="checkbox" checked={specials} onChange={(event) => setSpecials(event.target.checked)} /> Also monitor specials
      </label>
      <label className="flex items-center gap-2 text-xs text-ink-muted">
        <span className="w-20" />
        <input type="checkbox" checked={searchMissing} onChange={(event) => setSearchMissing(event.target.checked)} /> Start search for missing
        episodes
      </label>
      <div className="flex items-center gap-3">
        <span className="w-20" />
        <button
          type="button"
          disabled={add.isPending}
          onClick={() => add.mutate()}
          className="rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink disabled:opacity-50"
        >
          {add.isPending ? "Adding…" : `Add ${candidate.title}`}
        </button>
        {add.isError && <span className="text-xs text-danger">It could not be added. The metadata sources may be unreachable; try again.</span>}
      </div>
    </div>
  );
}

/** Add series to a TV library: search the metadata sources, pick what to monitor, add. */
export function AddSeries() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  const debounced = useDebouncedValue(query.trim(), 300);

  const results = useQuery({
    queryKey: ["series-lookup", categoryId, debounced],
    enabled: debounced.length >= 2,
    queryFn: async () => {
      const { data, error } = await api.GET("/series/lookup", {
        params: { query: { category_id: Number(categoryId), q: debounced, limit: 20 } },
      });
      if (error) throw error;
      return data;
    },
  });

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-4 p-4">
      <h1 className="text-base font-semibold">Add series</h1>
      <label className="relative flex items-center">
        <Search aria-hidden className="pointer-events-none absolute left-3 size-4 text-ink-muted" />
        <input
          autoFocus
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Search by name, or tvdb:12345"
          aria-label="Search for a series"
          className="w-full rounded-md border border-border-subtle bg-surface py-2 pl-9 pr-3 text-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        />
      </label>
      {debounced.length < 2 && <p className="text-sm text-ink-muted">Type at least two letters.</p>}
      {results.isFetching && <p className="text-sm text-ink-muted">Searching…</p>}
      {results.isError && <p className="text-sm text-danger">The metadata sources could not be reached. Try again in a moment.</p>}
      {results.data && results.data.length === 0 && !results.isFetching && <p className="text-sm text-ink-muted">Nothing found for “{debounced}”.</p>}
      <ul className="flex flex-col gap-3">
        {(results.data ?? []).map((candidate) => {
          const key = `${candidate.provider}:${candidate.external_id}`;
          return (
            <li key={key} className="rounded-lg border border-border-subtle bg-surface p-3">
              <div className="flex gap-3">
                <div className="h-28 w-[75px] shrink-0 overflow-hidden rounded-md border border-border-subtle bg-canvas">
                  {candidate.poster_url ? (
                    <img src={remoteImage(candidate.poster_url, "thumb")} alt="" loading="lazy" className="size-full object-cover" />
                  ) : (
                    <span className="flex size-full items-center justify-center text-ink-muted">
                      <Tv aria-hidden className="size-6" />
                    </span>
                  )}
                </div>
                <div className="flex min-w-0 flex-1 flex-col gap-1">
                  <div className="flex flex-wrap items-baseline gap-2">
                    <span className="text-sm font-semibold">{candidate.title}</span>
                    <span className="text-xs text-ink-muted">
                      {[candidate.year, candidate.network, candidate.status].filter(Boolean).join(" · ")}
                    </span>
                    {candidate.provider === "tmdb" && (
                      <span className="rounded bg-canvas px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-ink-muted" title="TheTVDB does not list it; its episodes come from TMDB">
                        TMDB only
                      </span>
                    )}
                  </div>
                  {candidate.overview && <p className="line-clamp-3 text-xs text-ink-muted">{candidate.overview}</p>}
                  <div className="mt-auto">
                    {candidate.library_id != null ? (
                      <Link
                        to="/library/$categoryId/series/$seriesId"
                        params={{ categoryId, seriesId: String(candidate.library_id) }}
                        className="inline-flex items-center gap-1 text-xs text-ok"
                      >
                        <Check aria-hidden className="size-3.5" /> Already in your library
                      </Link>
                    ) : (
                      <button
                        type="button"
                        onClick={() => setOpen(open === key ? null : key)}
                        className="rounded-md border border-border-subtle px-2.5 py-1 text-xs hover:bg-canvas"
                      >
                        {open === key ? "Cancel" : "Add…"}
                      </button>
                    )}
                  </div>
                </div>
              </div>
              {open === key && <AddPanel candidate={candidate} categoryId={categoryId} onDone={() => setOpen(null)} />}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
