import { useEffect, useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Plus, Search } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

import { isSearchShortcut } from "@/lib/searchShortcut";

import { CoverArt } from "./CoverArt";
import { useDebouncedValue } from "./useDebouncedValue";

type SearchResult = components["schemas"]["SearchResult"];
type DiscoverResult = components["schemas"]["DiscoverResult"];
type Library = components["schemas"]["Library"];

// Media types Discover can search a provider for; music only until TV (stage 8).
const DISCOVERABLE = new Set(["music"]);
const NEW_PER_KIND = 5;

// Mirrors library.ErrQueryTooShort's floor (section 21.5), so a one-character keystroke
// never fires a request the server would just reject.
const MINIMUM_QUERY_LENGTH = 2;
const DEBOUNCE_MS = 250;

/** Type-ahead over the whole library (section 21.1), category-filtered
 *  server-side (ADR-27). "hero" is the dashboard's own large field; "header"
 *  the compact one in the status bar. A result links to its work -- it used
 *  not to, from a comment that outlived the /works/$id page it predates. */
export function SearchBox({ variant = "header" }: { variant?: "header" | "hero" }) {
  const [query, setQuery] = useState("");
  const [focused, setFocused] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const hero = variant === "hero";

  // "/" focuses the header box from anywhere, as in GitHub or Lidarr's own search (asked 2026-09-19).
  useEffect(() => {
    if (hero) return;
    const onKey = (event: KeyboardEvent) => {
      if (!isSearchShortcut(event)) return;
      event.preventDefault();
      input.current?.focus();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [hero]);
  const debounced = useDebouncedValue(query.trim(), DEBOUNCE_MS);
  const searchable = debounced.length >= MINIMUM_QUERY_LENGTH;

  const results = useQuery({
    queryKey: ["search", debounced],
    queryFn: async () => {
      const { data, error } = await api.GET("/search", {
        params: { query: { q: debounced, limit: 20 } },
      });
      if (error) throw error;
      return data;
    },
    enabled: searchable,
    staleTime: 30_000,
  });

  // Artists in the library too, not only album titles (found 2026-09-30: "Pendulum" matched nothing).
  const artists = useQuery({
    queryKey: ["search-artists", debounced],
    queryFn: async () => {
      const { data, error } = await api.GET("/people", { params: { query: { q: debounced, limit: 5 } } });
      if (error) throw error;
      return data.items;
    },
    enabled: searchable,
    staleTime: 30_000,
  });

  // Beyond the library (asked 2026-09-30, ADR-56's global half): artists and albums not in it yet,
  // searched in a library the person may add to; with several, they choose which.
  const libraries = useQuery({
    queryKey: ["libraries"],
    queryFn: async () => {
      const { data, error } = await api.GET("/libraries");
      if (error) throw error;
      return data;
    },
  });
  const addable = (libraries.data ?? []).filter(
    (library: Library) =>
      DISCOVERABLE.has(library.media_type) && (library.permission === "request" || library.permission === "manage"),
  );
  const [chosenLibrary, setChosenLibrary] = useState<number | undefined>();
  const destination = addable.find((library) => library.id === chosenLibrary) ?? addable[0];
  const discovered = useQuery({
    queryKey: ["search-new", destination?.id, debounced],
    queryFn: async () => {
      const found: DiscoverResult[] = [];
      for (const kind of ["artist", "album"] as const) {
        const { data, error } = await api.GET("/discover/search", {
          params: { query: { category_id: destination!.id, q: debounced, kind, limit: NEW_PER_KIND } },
        });
        if (error) throw error;
        found.push(...data.map((result) => ({ ...result, kind })));
      }
      return found;
    },
    enabled: searchable && destination !== undefined,
    staleTime: 60_000,
  });

  const open = focused && searchable;

  return (
    <div className={`relative ${hero ? "w-full" : "w-72"}`}>
      <Search
        aria-hidden
        className={`pointer-events-none absolute top-1/2 -translate-y-1/2 text-ink-muted ${
          hero ? "left-3.5 size-5" : "left-2.5 size-3.5"
        }`}
      />
      <input
        ref={input}
        type="search"
        aria-keyshortcuts={hero ? undefined : "/"}
        value={query}
        placeholder="Search the library…"
        onChange={(event) => setQuery(event.target.value)}
        onFocus={() => setFocused(true)}
        // A click on a result would otherwise blur the field and unmount the
        // list before the link ever fires.
        onBlur={() => window.setTimeout(() => setFocused(false), 120)}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            setQuery("");
            event.currentTarget.blur();
          }
        }}
        className={`w-full rounded-md border border-border-subtle bg-canvas placeholder:text-ink-muted
                    focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent ${
                      hero ? "py-3 pr-3 pl-11 text-base" : "py-1.5 pr-2 pl-8 text-sm"
                    }`}
      />

      {!hero && !focused && query === "" && (
        <kbd
          aria-hidden
          className="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 rounded border border-border-subtle
                     px-1.5 text-[0.6875rem] leading-4 text-ink-muted"
        >
          /
        </kbd>
      )}

      {open && (
        <ul
          className="absolute top-full left-0 z-10 mt-1 max-h-80 w-full overflow-y-auto
                     rounded-md border border-border-subtle bg-surface text-sm shadow-lg"
        >
          {results.isLoading && <li className="px-3 py-2 text-ink-muted">Searching…</li>}
          {results.error != null && <li className="px-3 py-2 text-danger">The search could not be completed.</li>}
          {results.data?.length === 0 && artists.data?.length === 0 && (
            <li className="px-3 py-2 text-ink-muted">No matches in your library.</li>
          )}
          {artists.data?.map((person) => (
            <li key={`person-${person.id}`} className="border-b border-border-subtle last:border-b-0">
              <Link
                to="/people/$id"
                params={{ id: String(person.id) }}
                className="flex items-center gap-2 px-3 py-1.5 hover:bg-canvas
                           focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent"
              >
                <CoverArt subject="people" workId={person.id} title={person.name} size="thumb" className="size-7 shrink-0 rounded-full object-cover" />
                <span className="truncate font-medium">{person.name}</span>
                <span className="ml-auto shrink-0 text-ink-muted">Artist</span>
              </Link>
            </li>
          ))}
          {results.data?.map((result) => <ResultRow key={result.work.id} result={result} />)}
          {destination && (
            <li className="flex items-center gap-2 border-t border-border-subtle bg-canvas px-3 py-1.5 text-xs text-ink-muted">
              <span className="flex-1">Not in your library yet</span>
              {addable.length > 1 && (
                <select
                  aria-label="Add to which library"
                  value={destination.id}
                  onMouseDown={(event) => event.stopPropagation()}
                  onChange={(event) => setChosenLibrary(Number(event.target.value))}
                  className="rounded border border-border-subtle bg-canvas px-1 py-0.5"
                >
                  {addable.map((library) => (
                    <option key={library.id} value={library.id}>
                      {library.name}
                    </option>
                  ))}
                </select>
              )}
            </li>
          )}
          {destination && discovered.isLoading && <li className="px-3 py-2 text-ink-muted">Searching the metadata sources…</li>}
          {destination && discovered.error != null && (
            <li className="px-3 py-2 text-danger">The metadata sources could not be searched.</li>
          )}
          {destination && discovered.data?.length === 0 && <li className="px-3 py-2 text-ink-muted">Nothing new found.</li>}
          {destination &&
            discovered.data?.map((result) => (
              <NewResultRow key={`${result.kind}-${result.external_id}`} result={result} libraryId={destination.id} query={debounced} />
            ))}
        </ul>
      )}
    </div>
  );
}

function ResultRow({ result }: { result: SearchResult }) {
  const { work } = result;
  return (
    <li className="border-b border-border-subtle last:border-b-0">
      <Link
        to="/works/$id"
        params={{ id: String(work.id) }}
        className="flex items-center gap-2 px-3 py-1.5 hover:bg-canvas
                   focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent"
      >
        <CoverArt workId={work.id} title={work.title} size="thumb" className="size-7 shrink-0 rounded object-cover" />
        <span className="truncate font-medium">{work.title}</span>
        <span className="ml-auto shrink-0 text-ink-muted">
          {work.media_type}
          {work.year ? ` · ${work.year}` : ""}
        </span>
      </Link>
    </li>
  );
}

/** Something to add: opens the library's Discover with the search filled in, where the add options live. */
function NewResultRow({ result, libraryId, query }: { result: DiscoverResult; libraryId: number; query: string }) {
  const artist = result.kind === "artist";
  return (
    <li className="border-b border-border-subtle last:border-b-0">
      <Link
        to="/library/$categoryId/discover"
        params={{ categoryId: String(libraryId) }}
        search={{ q: query, kind: result.kind ?? "album" }}
        className="flex items-center gap-2 px-3 py-1.5 hover:bg-canvas
                   focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent"
      >
        <span className="flex size-7 shrink-0 items-center justify-center rounded border border-dashed border-border-subtle text-ink-muted">
          <Plus aria-hidden className="size-3.5" />
        </span>
        <span className="truncate font-medium">{result.title}</span>
        <span className="truncate text-ink-muted">
          {artist ? (result.disambiguation ?? "") : [result.artist, result.year].filter(Boolean).join(" · ")}
        </span>
        <span className="ml-auto shrink-0 text-xs text-ink-muted">{artist ? "Artist" : "Album"} · Add</span>
      </Link>
    </li>
  );
}
