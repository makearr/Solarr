import { useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Search } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

import { SeasonSearchDialog } from "./SeasonSearch";

type Episode = components["schemas"]["WantedEpisode"];

function episodeLabel(episode: Episode): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `S${pad(episode.season)}E${pad(episode.number)}`;
}

type View = "missing" | "cutoff";

/** Sonarr's Wanted for a TV library: Missing (monitored episodes that have aired and have no file) and Cutoff unmet (files below the target). */
export function SeriesWanted() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const [view, setView] = useState<View>("missing");
  const queryClient = useQueryClient();
  const [dialog, setDialog] = useState<Episode | null>(null);
  const [notes, setNotes] = useState<Record<string, string>>({});

  const pages = useInfiniteQuery({
    queryKey: ["series", categoryId, "wanted", view],
    initialPageParam: "",
    queryFn: async ({ pageParam }) => {
      const { data, error } = await api.GET("/series/wanted", {
        params: { query: { category_id: Number(categoryId), limit: 100, view, ...(pageParam ? { cursor: pageParam } : {}) } },
      });
      if (error) throw error;
      return data;
    },
    getNextPageParam: (last) => last.next_cursor || undefined,
  });
  const rows = (pages.data?.pages ?? []).flatMap((page) => page.items);
  const judged = pages.data?.pages[0]?.judged !== false;

  const grab = useMutation({
    mutationFn: async (episode: Episode) => {
      const { data, error } = await api.POST("/series/{id}/seasons/{season}/grab", {
        params: { path: { id: episode.series_id, season: episode.season }, query: { category_id: Number(categoryId) } },
      });
      if (error) throw error;
      return { episode, data };
    },
    onSuccess: ({ episode, data }) => {
      const note = data.sent ? "Sent to the download client" : data.already_downloading ? "Already downloading" : data.held ? "Held for room" : data.found === 0 ? "Nothing found" : "Found releases, none could be sent";
      setNotes((current) => ({ ...current, [`${episode.series_id}:${episode.season}`]: note }));
      void queryClient.invalidateQueries({ queryKey: ["series", categoryId] });
    },
    onError: (_error, episode) =>
      setNotes((current) => ({ ...current, [`${episode.series_id}:${episode.season}`]: "Search failed: is an indexer connected?" })),
  });

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-3 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="mr-2 text-base font-semibold">Wanted</h1>
        {(["missing", "cutoff"] as const).map((key) => (
          <button
            key={key}
            type="button"
            aria-pressed={view === key}
            onClick={() => setView(key)}
            className={`rounded-full border px-3 py-1 text-xs ${view === key ? "border-accent bg-accent/10 text-ink" : "border-border-subtle text-ink-muted hover:text-ink"}`}
          >
            {key === "missing" ? "Missing" : "Cutoff unmet"}
          </button>
        ))}
      </div>
      <p className="text-xs text-ink-muted">
        {view === "missing"
          ? "Monitored episodes that have aired and are not on disk. Search looks for the whole season, so a pack can fill several rows at once."
          : "Episodes on disk whose file is below the library's target or cutoff (Quality page). Interactive search picks a better release for one episode."}
      </p>
      {view === "cutoff" && !judged && (
        <p className="text-sm text-ink-muted">
          This library has no target or cutoff yet, so nothing can be below it. Set one on its{" "}
          <Link to="/library/$categoryId/config/quality" params={{ categoryId }} className="text-accent underline">
            Quality page
          </Link>
          .
        </p>
      )}
      {pages.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {pages.error !== null && !pages.isLoading && <p className="text-sm text-danger">The list could not be read.</p>}
      {!pages.isLoading && rows.length === 0 && pages.error === null && (view === "missing" || judged) && (
        <p className="text-sm text-ink-muted">{view === "missing" ? "Nothing is missing." : "Every measured file meets the target."}</p>
      )}
      <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
        {rows.map((episode) => {
          const note = notes[`${episode.series_id}:${episode.season}`];
          return (
            <li key={episode.part_id} className="flex flex-wrap items-center gap-3 px-3 py-2 text-sm">
              <Link
                to="/library/$categoryId/series/$seriesId"
                params={{ categoryId, seriesId: String(episode.series_id) }}
                className="w-56 truncate font-medium hover:underline"
              >
                {episode.series_title}
              </Link>
              <span className="w-16 tabular-nums text-ink-muted">{episodeLabel(episode)}</span>
              <span className="min-w-0 flex-1 truncate">{episode.title ?? "TBA"}</span>
              <span className="w-24 text-xs text-ink-muted">{episode.air_date ?? ""}</span>
              {episode.file && (
                <span className="text-xs text-ink-muted">
                  {[episode.file.resolution, episode.file.video_codec?.toUpperCase(), episode.file.hdr?.toUpperCase()].filter(Boolean).join(" ")}
                </span>
              )}
              {note && <span className="text-xs text-ink-muted">{note}</span>}
              {view === "missing" && (
              <button
                type="button"
                title="Search this season and send the best release"
                aria-label={`Search ${episode.series_title} season ${episode.season}`}
                onClick={() => grab.mutate(episode)}
                className="text-ink-muted hover:text-ink"
              >
                <Search aria-hidden className="size-4" />
              </button>
              )}
              <button
                type="button"
                onClick={() => setDialog(episode)}
                className="rounded-md border border-border-subtle px-2 py-0.5 text-xs text-ink-muted hover:text-ink"
              >
                Interactive
              </button>
            </li>
          );
        })}
      </ul>
      {pages.hasNextPage && (
        <button
          type="button"
          disabled={pages.isFetchingNextPage}
          onClick={() => void pages.fetchNextPage()}
          className="self-start rounded-md border border-border-subtle px-3 py-1.5 text-sm disabled:opacity-50"
        >
          {pages.isFetchingNextPage ? "Loading…" : "Load more"}
        </button>
      )}
      {dialog && (
        <SeasonSearchDialog
          categoryId={Number(categoryId)}
          seriesId={dialog.series_id}
          season={dialog.season}
          episode={view === "cutoff" ? dialog.number : undefined}
          title={view === "cutoff" ? `${dialog.series_title} ${episodeLabel(dialog)}` : `${dialog.series_title}, season ${dialog.season}`}
          onClose={() => setDialog(null)}
        />
      )}
    </div>
  );
}
