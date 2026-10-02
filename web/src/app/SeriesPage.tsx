import { useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bookmark, BookmarkCheck, ChevronDown, ChevronRight, RefreshCw, Search, Tv, User } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { useLibrary } from "@/lib/useLibrary";

import { SeasonSearchDialog } from "./SeasonSearch";

type Episode = components["schemas"]["SeriesEpisode"];
type Season = components["schemas"]["SeriesSeason"];

const STATE: Record<Episode["state"], { label: string; tone: string }> = {
  present: { label: "On disk", tone: "text-ok" },
  missing: { label: "Missing", tone: "text-danger" },
  unaired: { label: "Unaired", tone: "text-ink-muted" },
  unmonitored: { label: "Unmonitored", tone: "text-ink-muted" },
};

function airDate(episode: Episode): string {
  if (episode.air_time) return new Date(episode.air_time).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
  return episode.air_date ?? "TBA";
}

function seasonLabel(number: number): string {
  return number === 0 ? "Specials" : `Season ${number}`;
}

function SeasonSection({
  season,
  episodes,
  canManage,
  onSeason,
  onEpisode,
  onAutoSearch,
  onInteractive,
  searchNote,
  initiallyOpen,
}: {
  season: Season;
  episodes: Episode[];
  canManage: boolean;
  onSeason: (monitored: boolean) => void;
  onEpisode: (episode: Episode, monitored: boolean) => void;
  onAutoSearch: () => void;
  onInteractive: () => void;
  searchNote: string | undefined;
  initiallyOpen: boolean;
}) {
  const [open, setOpen] = useState(initiallyOpen);
  const Chevron = open ? ChevronDown : ChevronRight;
  const { monitored, present } = season.counts;
  return (
    <section className="rounded-lg border border-border-subtle bg-surface">
      <header className="flex items-center gap-3 px-3 py-2">
        <button
          type="button"
          aria-label={season.monitored ? `Unmonitor ${seasonLabel(season.number)}` : `Monitor ${seasonLabel(season.number)}`}
          disabled={!canManage}
          onClick={() => onSeason(!season.monitored)}
          className="text-ink-muted enabled:hover:text-ink disabled:opacity-50"
        >
          {season.monitored ? <BookmarkCheck aria-hidden className="size-4 text-accent" /> : <Bookmark aria-hidden className="size-4" />}
        </button>
        <button type="button" onClick={() => setOpen(!open)} className="flex flex-1 items-center gap-2 text-left" aria-expanded={open}>
          <Chevron aria-hidden className="size-4 text-ink-muted" />
          <span className="text-sm font-semibold">{seasonLabel(season.number)}</span>
          <span className="text-xs text-ink-muted">
            {present}/{monitored} · {episodes.length} episodes
          </span>
        </button>
        {searchNote && <span className="text-xs text-ink-muted">{searchNote}</span>}
        <button
          type="button"
          title="Search this season and send the best release"
          aria-label={`Search ${seasonLabel(season.number)}`}
          disabled={!canManage}
          onClick={onAutoSearch}
          className="text-ink-muted enabled:hover:text-ink disabled:opacity-50"
        >
          <Search aria-hidden className="size-4" />
        </button>
        <button
          type="button"
          title="Interactive search: choose the release yourself"
          aria-label={`Interactive search ${seasonLabel(season.number)}`}
          disabled={!canManage}
          onClick={onInteractive}
          className="text-ink-muted enabled:hover:text-ink disabled:opacity-50"
        >
          <User aria-hidden className="size-4" />
        </button>
      </header>
      {open && (
        <table className="w-full border-t border-border-subtle text-sm">
          <tbody>
            {episodes.map((episode) => (
              <tr key={episode.id} className="border-b border-border-subtle last:border-0">
                <td className="w-9 px-3 py-1.5">
                  <button
                    type="button"
                    aria-label={episode.monitored ? "Unmonitor episode" : "Monitor episode"}
                    disabled={!canManage}
                    onClick={() => onEpisode(episode, !episode.monitored)}
                    className="text-ink-muted enabled:hover:text-ink disabled:opacity-50"
                  >
                    {episode.monitored ? <BookmarkCheck aria-hidden className="size-4 text-accent" /> : <Bookmark aria-hidden className="size-4" />}
                  </button>
                </td>
                <td className="w-10 py-1.5 tabular-nums text-ink-muted">{episode.number}</td>
                <td className="py-1.5">
                  <span>{episode.title ?? "TBA"}</span>
                  {episode.absolute != null && <span className="ml-2 text-xs text-ink-muted">#{episode.absolute}</span>}
                </td>
                <td className="hidden whitespace-nowrap py-1.5 pr-3 text-xs text-ink-muted sm:table-cell">{airDate(episode)}</td>
                <td className={`whitespace-nowrap py-1.5 pr-3 text-right text-xs ${STATE[episode.state].tone}`}>{STATE[episode.state].label}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

/** One series: its facts, then every season with its episodes (Sonarr's series page). */
export function SeriesPage() {
  const { categoryId, seriesId } = useParams({ strict: false }) as { categoryId: string; seriesId: string };
  const queryClient = useQueryClient();
  const canManage = useLibrary(categoryId)?.permission === "manage";
  const key = ["series", categoryId, seriesId];

  const detail = useQuery({
    queryKey: key,
    queryFn: async () => {
      const { data, error } = await api.GET("/series/{id}", {
        params: { path: { id: Number(seriesId) }, query: { category_id: Number(categoryId) } },
      });
      if (error) throw error;
      return data;
    },
  });
  const changed = () => {
    void queryClient.invalidateQueries({ queryKey: key });
    void queryClient.invalidateQueries({ queryKey: ["series", categoryId] });
  };
  const refresh = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/series/{id}/refresh", {
        params: { path: { id: Number(seriesId) }, query: { category_id: Number(categoryId) } },
      });
      if (error) throw error;
    },
    onSuccess: changed,
  });
  const setSeason = useMutation({
    mutationFn: async (target: { season: number; monitored: boolean }) => {
      const { error } = await api.PUT("/series/{id}/seasons/{season}/monitored", {
        params: { path: { id: Number(seriesId), season: target.season }, query: { category_id: Number(categoryId) } },
        body: { monitored: target.monitored },
      });
      if (error) throw error;
    },
    onSuccess: changed,
  });
  const setEpisode = useMutation({
    mutationFn: async (target: { episode: Episode; monitored: boolean }) => {
      const { error } = await api.PUT("/series/{id}/episodes/{part_id}/monitored", {
        params: { path: { id: Number(seriesId), part_id: target.episode.id }, query: { category_id: Number(categoryId) } },
        body: { monitored: target.monitored },
      });
      if (error) throw error;
    },
    onSuccess: changed,
  });

  const [dialogSeason, setDialogSeason] = useState<number | null>(null);
  const [notes, setNotes] = useState<Record<number, string>>({});
  const grab = useMutation({
    mutationFn: async (season: number) => {
      const { data, error } = await api.POST("/series/{id}/seasons/{season}/grab", {
        params: { path: { id: Number(seriesId), season }, query: { category_id: Number(categoryId) } },
      });
      if (error) throw error;
      return { season, data };
    },
    onSuccess: ({ season, data }) => {
      const note = data.sent ? "Sent to the download client" : data.already_downloading ? "Already downloading" : data.held ? "Held for room in the client's queue" : data.found === 0 ? "Nothing found" : "Found releases, none could be sent";
      setNotes((current) => ({ ...current, [season]: note }));
      changed();
    },
    onError: (_error, season) => setNotes((current) => ({ ...current, [season]: "Search failed: is an indexer connected, and is anything missing?" })),
  });

  if (detail.isLoading) return <p className="p-4 text-sm text-ink-muted">Loading…</p>;
  if (detail.isError || !detail.data) return <p className="p-4 text-sm text-danger">This series could not be found.</p>;

  const series = detail.data.summary;
  const bySeason = new Map<number, Episode[]>();
  for (const episode of detail.data.episodes) bySeason.set(episode.season, [...(bySeason.get(episode.season) ?? []), episode]);
  const seasons = [...detail.data.seasons].sort((a, b) => (a.number === 0 ? 1 : b.number === 0 ? -1 : b.number - a.number));
  const newest = seasons.find((season) => season.number > 0)?.number;
  const facts = [
    series.year,
    series.network,
    series.status,
    detail.data.runtime_minutes ? `${detail.data.runtime_minutes} min` : null,
    detail.data.air_time,
    ...(series.genres ?? []),
  ].filter(Boolean);
  const links: [string, string, string][] = [
    ["tvdb", "TheTVDB", `https://thetvdb.com/dereferrer/series/${detail.data.ids.tvdb}`],
    ["tvmaze", "TVmaze", `https://www.tvmaze.com/shows/${detail.data.ids.tvmaze}`],
    ["imdb", "IMDb", `https://www.imdb.com/title/${detail.data.ids.imdb}/`],
    ["tmdb", "TMDB", `https://www.themoviedb.org/tv/${detail.data.ids.tmdb}`],
  ];

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-4 p-4">
      <Link to="/library/$categoryId/$level" params={{ categoryId, level: "series" }} className="text-xs text-ink-muted hover:text-ink">
        ← Series
      </Link>
      <div className="flex gap-4">
        <div className="h-60 w-40 shrink-0 overflow-hidden rounded-lg border border-border-subtle bg-surface">
          {series.poster_url ? (
            <img src={series.poster_url} alt="" className="size-full object-cover" referrerPolicy="no-referrer" />
          ) : (
            <span className="flex size-full items-center justify-center text-ink-muted">
              <Tv aria-hidden className="size-10" />
            </span>
          )}
        </div>
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          <h1 className="text-xl font-semibold">{series.title}</h1>
          <p className="text-sm text-ink-muted">{facts.join(" · ")}</p>
          <p className="text-sm">
            <span className="font-medium">{series.counts.present}</span> of {series.counts.monitored} monitored episodes on disk
            {series.counts.aired_missing > 0 && <span className="text-danger"> · {series.counts.aired_missing} aired and missing</span>}
          </p>
          {series.overview && <p className="max-w-3xl text-sm text-ink-muted">{series.overview}</p>}
          <div className="mt-auto flex flex-wrap items-center gap-3 text-xs">
            {canManage && (
              <button
                type="button"
                disabled={refresh.isPending}
                onClick={() => refresh.mutate()}
                className="flex items-center gap-1 rounded-md border border-border-subtle px-2.5 py-1 text-ink-muted enabled:hover:text-ink disabled:opacity-50"
              >
                <RefreshCw aria-hidden className={`size-3.5 ${refresh.isPending ? "animate-spin" : ""}`} /> Refresh
              </button>
            )}
            {refresh.isError && <span className="text-danger">Refresh failed.</span>}
            {links
              .filter(([id]) => detail.data.ids[id])
              .map(([, label, href]) => (
                <a key={label} href={href} target="_blank" rel="noreferrer" className="text-accent hover:underline">
                  {label}
                </a>
              ))}
          </div>
        </div>
      </div>
      {seasons.map((season) => (
        <SeasonSection
          key={season.number}
          season={season}
          episodes={bySeason.get(season.number) ?? []}
          canManage={canManage}
          initiallyOpen={season.number === newest}
          onSeason={(monitored) => setSeason.mutate({ season: season.number, monitored })}
          onEpisode={(episode, monitored) => setEpisode.mutate({ episode, monitored })}
          onAutoSearch={() => grab.mutate(season.number)}
          onInteractive={() => setDialogSeason(season.number)}
          searchNote={grab.isPending && grab.variables === season.number ? "Searching…" : notes[season.number]}
        />
      ))}
      {dialogSeason !== null && (
        <SeasonSearchDialog
          categoryId={Number(categoryId)}
          seriesId={Number(seriesId)}
          season={dialogSeason}
          title={`${series.title}, ${seasonLabel(dialogSeason)}`}
          onClose={() => setDialogSeason(null)}
        />
      )}
    </div>
  );
}
