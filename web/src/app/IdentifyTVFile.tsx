import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";

import { api } from "@/api/client";
import { remoteImage } from "@/lib/remoteImage";

import { useDebouncedValue } from "./useDebouncedValue";

/** "5" or "2, 3" or "2-4" as the episode numbers they stand for; null when it is not a list of numbers. */
export function parseEpisodes(text: string): number[] | null {
  const numbers: number[] = [];
  for (const part of text.split(",").map((piece) => piece.trim()).filter(Boolean)) {
    const range = /^(\d+)\s*-\s*(\d+)$/.exec(part);
    if (range) {
      const [from, to] = [Number(range[1]), Number(range[2])];
      if (to < from || to - from > 60) return null;
      for (let n = from; n <= to; n++) numbers.push(n);
    } else if (/^\d+$/.test(part)) numbers.push(Number(part));
    else return null;
  }
  return numbers;
}

/** Place a queued TV file: choose the series, and the season and episodes when the file's name does not say. */
export function IdentifyTVFile({
  categoryId,
  mediaObjectId,
  relPath,
  wholeFolder,
  onDone,
}: {
  categoryId: number;
  mediaObjectId: number;
  relPath: string;
  wholeFolder: boolean;
  onDone: () => void;
}) {
  const [typed, setTyped] = useState(() => (relPath.split("/")[0] ?? "").replace(/[([{].*$/, "").trim());
  const query = useDebouncedValue(typed.trim(), 300);
  const [chosen, setChosen] = useState<{ provider: "tvdb" | "tmdb"; external_id: string; title: string } | null>(null);
  const [season, setSeason] = useState("");
  const [episodes, setEpisodes] = useState("");

  const results = useQuery({
    queryKey: ["series-lookup", categoryId, query],
    enabled: query.length >= 2,
    queryFn: async () => {
      const { data, error } = await api.GET("/series/lookup", { params: { query: { category_id: categoryId, q: query, limit: 8 } } });
      if (error) throw error;
      return data;
    },
  });
  const parsed = parseEpisodes(episodes);
  const explicit = episodes.trim() !== "";
  const place = useMutation({
    mutationFn: async () => {
      if (!chosen) return;
      const { error } = await api.POST("/library/unidentified/{mediaObjectId}/resolve", {
        params: { path: { mediaObjectId } },
        body: {
          provider: chosen.provider,
          external_id: chosen.external_id,
          whole_folder: wholeFolder,
          ...(explicit && parsed ? { season: Number(season || 1), episodes: parsed } : {}),
        },
      });
      if (error) throw error;
    },
    onSuccess: onDone,
  });
  const input = "rounded-md border border-border-subtle bg-canvas px-2 py-1 text-sm text-ink";
  return (
    <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3 text-sm">
      <h2 className="text-sm font-medium">Which series is this?</h2>
      <label className="relative flex items-center">
        <Search aria-hidden className="pointer-events-none absolute left-2 size-3.5 text-ink-muted" />
        <input value={typed} onChange={(event) => setTyped(event.target.value)} aria-label="Search for the series" className={`${input} w-full pl-7`} />
      </label>
      <ul className="flex flex-col gap-1">
        {(results.data ?? []).map((candidate) => {
          const picked = chosen?.external_id === candidate.external_id && chosen.provider === (candidate.provider === "tmdb" ? "tmdb" : "tvdb");
          return (
            <li key={`${candidate.provider}:${candidate.external_id}`}>
              <button
                type="button"
                onClick={() => setChosen({ provider: candidate.provider === "tmdb" ? "tmdb" : "tvdb", external_id: candidate.external_id, title: candidate.title })}
                className={`flex w-full items-center gap-2 rounded-md border px-2 py-1 text-left ${picked ? "border-accent bg-accent/10" : "border-border-subtle hover:bg-canvas"}`}
              >
                {candidate.poster_url && <img src={remoteImage(candidate.poster_url, "thumb")} alt="" className="h-10 w-7 rounded object-cover" />}
                <span className="flex-1 truncate">
                  {candidate.title}
                  <span className="ml-2 text-xs text-ink-muted">{[candidate.year, candidate.network].filter(Boolean).join(" · ")}</span>
                </span>
              </button>
            </li>
          );
        })}
      </ul>
      <div className="flex flex-wrap items-end gap-3 text-xs text-ink-muted">
        <label className="flex flex-col gap-1">
          Season
          <input value={season} onChange={(event) => setSeason(event.target.value.replace(/\D/g, ""))} placeholder="from the name" className={`${input} w-24`} />
        </label>
        <label className="flex flex-col gap-1">
          Episodes
          <input value={episodes} onChange={(event) => setEpisodes(event.target.value)} placeholder="from the name, or 3 or 3-4" className={`${input} w-48`} />
        </label>
        <span className="max-w-md">Leave both empty when the file name says which episode it is. Season 0 is the specials.</span>
      </div>
      <div className="flex items-center gap-3">
        <button
          type="button"
          disabled={!chosen || place.isPending || (explicit && !parsed)}
          onClick={() => place.mutate()}
          className="rounded-md bg-accent px-3 py-1.5 text-accent-ink disabled:opacity-50"
        >
          {place.isPending ? "Placing…" : chosen ? `Place it in ${chosen.title}` : "Choose a series"}
        </button>
        {place.isError && <span className="text-xs text-danger">{(place.error as { detail?: string }).detail ?? "That could not be placed."}</span>}
      </div>
    </section>
  );
}
