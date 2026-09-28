import { useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type DiscoverResult = components["schemas"]["DiscoverResult"];

const MINIMUM_QUERY_LENGTH = 2;

const DISC_FOLDER = /^(cd|disc|disk)\s*\d+$/i;

/** "Artist/Album (2011)/CD1/01.flac" -> "Artist Album": the folders are the best first guess. */
export function queryFromPath(relPath: string): string {
  const folders = relPath
    .split("/")
    .slice(0, -1)
    .filter((folder) => !DISC_FOLDER.test(folder.trim()))
    .slice(-2);
  return folders
    .map((folder) => folder.replace(/\s*[([]\d{4}[)\]]\s*$/, "").trim())
    .filter(Boolean)
    .join(" ");
}

/** Lidarr's manual import: search the metadata source by hand when no pass found the album. */
export function ManualAlbumSearch({
  relPath,
  onChoose,
  busy,
}: {
  relPath: string;
  onChoose: (result: DiscoverResult) => void;
  busy: boolean;
}) {
  const [draft, setDraft] = useState(() => queryFromPath(relPath));
  const [query, setQuery] = useState("");

  // The library only selects the media type searched; the queue holds music files.
  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: async () => {
      const { data, error } = await api.GET("/categories");
      if (error) throw error;
      return data;
    },
  });
  const categoryId = categories.data?.find((category) => category.media_type === "music")?.id;

  const results = useQuery({
    queryKey: ["discover-search", categoryId, "album", query],
    queryFn: async () => {
      const { data, error } = await api.GET("/discover/search", {
        params: { query: { category_id: categoryId as number, q: query, kind: "album", limit: 20 } },
      });
      if (error) throw error;
      return data;
    },
    enabled: categoryId !== undefined && query.length >= MINIMUM_QUERY_LENGTH,
  });

  return (
    <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3 text-xs">
      <h2 className="font-medium text-ink">Search for the album</h2>
      <form
        className="flex gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          setQuery(draft.trim());
        }}
      >
        <input
          aria-label="Artist and album"
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          placeholder="Artist and album"
          className="min-w-0 flex-1 rounded-md border border-border-subtle bg-canvas px-2 py-1 text-sm text-ink"
        />
        <button
          type="submit"
          disabled={draft.trim().length < MINIMUM_QUERY_LENGTH || categoryId === undefined}
          className="rounded-md bg-accent px-3 py-1 text-accent-ink disabled:opacity-50"
        >
          Search
        </button>
      </form>

      {results.isFetching && <p className="text-ink-muted">Searching…</p>}
      {results.isError && <p className="text-danger">The search failed; try again in a moment.</p>}
      {results.data?.length === 0 && <p className="text-ink-muted">No album found for “{query}”.</p>}
      {results.data && results.data.length > 0 && (
        <ul className="flex flex-col divide-y divide-border-subtle">
          {results.data.map((result) => (
            <li key={`${result.provider}:${result.external_id}`} className="flex items-center gap-3 py-1.5">
              <span className="min-w-0 flex-1">
                <span className="text-ink">{result.title}</span>
                {result.artist && <span className="text-ink-muted"> · {result.artist}</span>}
                {result.year !== undefined && <span className="text-ink-muted"> · {result.year}</span>}
                {result.disambiguation && <span className="text-ink-muted"> ({result.disambiguation})</span>}
              </span>
              <button
                type="button"
                disabled={busy}
                onClick={() => onChoose(result)}
                className="shrink-0 rounded-md border border-border-subtle px-2 py-0.5 text-ink hover:border-accent disabled:opacity-50"
              >
                This album
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
