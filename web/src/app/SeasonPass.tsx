import { useMemo, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Bookmark, BookmarkCheck, Search } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { useLibrary } from "@/lib/useLibrary";

import { seasonGroup } from "./SeasonSearch";

type Series = components["schemas"]["SeriesSummary"];

/** Sonarr's Season Pass: every series with a switch for the whole series and one chip per season, so what is wanted
 *  can be changed across the library without opening each series. */
export function SeasonPass() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const canManage = useLibrary(categoryId)?.permission === "manage";
  const queryClient = useQueryClient();
  const [filter, setFilter] = useState("");
  const [onlyMonitored, setOnlyMonitored] = useState(false);

  const list = useInfiniteQuery({
    queryKey: ["series", categoryId, "pass"],
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam }) => {
      const { data, error } = await api.GET("/series", { params: { query: { category_id: Number(categoryId), cursor: pageParam, limit: 500 } } });
      if (error) throw error;
      return data;
    },
    getNextPageParam: (last) => last.next_cursor,
  });
  const changed = () => void queryClient.invalidateQueries({ queryKey: ["series", categoryId] });

  const setSeason = useMutation({
    mutationFn: async (target: { id: number; season: number; monitored: boolean }) => {
      const { error } = await api.POST("/works/{id}/monitor", {
        params: { path: { id: target.id } },
        body: { monitored: target.monitored, group: seasonGroup(target.season) },
      });
      if (error) throw error;
    },
    onSuccess: changed,
  });
  const setSeries = useMutation({
    mutationFn: async (target: { id: number; monitored: boolean }) => {
      const { error } = await api.POST("/works/{id}/monitor", {
        params: { path: { id: target.id } },
        body: { option: target.monitored ? "all" : "none" },
      });
      if (error) throw error;
    },
    onSuccess: changed,
  });

  const rows = useMemo(() => {
    const all: Series[] = list.data?.pages.flatMap((page) => page.items) ?? [];
    const needle = filter.trim().toLowerCase();
    return all.filter((row) => (!needle || row.title.toLowerCase().includes(needle)) && (!onlyMonitored || row.monitored));
  }, [list.data, filter, onlyMonitored]);

  return (
    <div className="flex flex-col gap-3 p-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold">Season pass</h1>
        <p className="text-xs text-ink-muted">Choose what to want, series by series and season by season.</p>
        <label className="ml-auto flex items-center gap-1.5 rounded-md border border-border-subtle bg-canvas px-2 py-1 text-xs text-ink-muted">
          <Search aria-hidden className="size-3.5" />
          <input value={filter} onChange={(event) => setFilter(event.target.value)} placeholder="Filter series…" aria-label="Filter series" className="w-44 bg-transparent text-ink outline-none" />
        </label>
        <label className="flex items-center gap-1.5 text-xs text-ink-muted">
          <input type="checkbox" checked={onlyMonitored} onChange={(event) => setOnlyMonitored(event.target.checked)} /> Monitored only
        </label>
      </div>
      {list.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {list.isError && <p className="text-sm text-danger">The series could not be loaded.</p>}
      <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
        {rows.map((row) => (
          <li key={row.id} className="flex flex-wrap items-center gap-3 px-3 py-2">
            <button
              type="button"
              aria-label={row.monitored ? `Unmonitor ${row.title}` : `Monitor ${row.title}`}
              title={row.monitored ? "Stop wanting this series" : "Want every episode of this series"}
              disabled={!canManage || setSeries.isPending}
              onClick={() => setSeries.mutate({ id: row.id, monitored: !row.monitored })}
              className="text-ink-muted enabled:hover:text-ink disabled:opacity-50"
            >
              {row.monitored ? <BookmarkCheck aria-hidden className="size-4 text-accent" /> : <Bookmark aria-hidden className="size-4" />}
            </button>
            <Link to="/library/$categoryId/series/$seriesId" params={{ categoryId, seriesId: String(row.id) }} className="min-w-40 flex-1 truncate text-sm hover:text-accent">
              {row.title}
              <span className="ml-2 text-xs text-ink-muted">{[row.year, row.network, row.status].filter(Boolean).join(" · ")}</span>
            </Link>
            <span className="flex flex-wrap gap-1">
              {[...(row.season_states ?? [])]
                .sort((a, b) => (a.number === 0 ? 1 : b.number === 0 ? -1 : a.number - b.number))
                .map((season) => (
                  <button
                    key={season.number}
                    type="button"
                    aria-pressed={season.monitored}
                    title={`${season.number === 0 ? "Specials" : `Season ${season.number}`}: ${season.monitored ? "monitored" : "not monitored"}`}
                    disabled={!canManage || setSeason.isPending}
                    onClick={() => setSeason.mutate({ id: row.id, season: season.number, monitored: !season.monitored })}
                    className={`min-w-8 rounded border px-1.5 py-0.5 text-xs tabular-nums disabled:opacity-60 ${
                      season.monitored ? "border-accent bg-accent/10 text-ink" : "border-border-subtle text-ink-muted hover:text-ink"
                    }`}
                  >
                    {season.number === 0 ? "Sp" : season.number}
                  </button>
                ))}
            </span>
          </li>
        ))}
      </ul>
      {list.hasNextPage && (
        <button type="button" onClick={() => void list.fetchNextPage()} className="mx-auto rounded-md border border-border-subtle px-4 py-1.5 text-sm text-ink-muted hover:text-ink">
          Load more series
        </button>
      )}
    </div>
  );
}
