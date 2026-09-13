import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

import { CoverArt } from "./CoverArt";
import { useDebouncedValue } from "./useDebouncedValue";

type SearchResult = components["schemas"]["SearchResult"];

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

  const open = focused && searchable;
  const hero = variant === "hero";

  return (
    <div className={`relative ${hero ? "w-full" : "w-72"}`}>
      <Search
        aria-hidden
        className={`pointer-events-none absolute top-1/2 -translate-y-1/2 text-ink-muted ${
          hero ? "left-3.5 size-5" : "left-2.5 size-3.5"
        }`}
      />
      <input
        type="search"
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

      {open && (
        <ul
          className="absolute top-full left-0 z-10 mt-1 max-h-80 w-full overflow-y-auto
                     rounded-md border border-border-subtle bg-surface text-sm shadow-lg"
        >
          {results.isLoading && <li className="px-3 py-2 text-ink-muted">Searching…</li>}
          {results.error != null && <li className="px-3 py-2 text-danger">The search could not be completed.</li>}
          {results.data?.length === 0 && <li className="px-3 py-2 text-ink-muted">No matches.</li>}
          {results.data?.map((result) => <ResultRow key={result.work.id} result={result} />)}
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
