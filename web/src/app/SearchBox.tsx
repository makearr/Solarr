import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

import { useDebouncedValue } from "./useDebouncedValue";

type SearchResult = components["schemas"]["SearchResult"];

// Mirrors library.ErrQueryTooShort's floor (section 21.5), so a one-character keystroke
// never fires a request the server would just reject.
const MINIMUM_QUERY_LENGTH = 2;
const DEBOUNCE_MS = 250;

/** Header type-ahead over the whole library (section 21.1), category-filtered
 *  server-side (ADR-27). Results are not links: a SearchResult carries no category id
 *  for /library's own filter, and a per-work detail page does not exist yet -- both are
 *  out of scope here. */
export function SearchBox() {
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

  return (
    <div className="relative w-72">
      <Search
        aria-hidden
        className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-ink-muted"
      />
      <input
        type="search"
        value={query}
        placeholder="Search the library…"
        onChange={(event) => setQuery(event.target.value)}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            setQuery("");
            event.currentTarget.blur();
          }
        }}
        className="w-full rounded-md border border-border-subtle bg-canvas py-1.5 pr-2 pl-8
                   text-sm placeholder:text-ink-muted
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      />

      {open && (
        <ul className="absolute top-full left-0 z-10 mt-1 max-h-80 w-full overflow-y-auto
                       rounded-md border border-border-subtle bg-surface text-sm shadow-lg">
          {results.isLoading && <li className="px-3 py-2 text-ink-muted">Searching…</li>}
          {results.error != null && (
            <li className="px-3 py-2 text-danger">The search could not be completed.</li>
          )}
          {results.data?.length === 0 && (
            <li className="px-3 py-2 text-ink-muted">No matches.</li>
          )}
          {results.data?.map((result) => <ResultRow key={result.work.id} result={result} />)}
        </ul>
      )}
    </div>
  );
}

function ResultRow({ result }: { result: SearchResult }) {
  const { work } = result;
  return (
    <li className="flex items-baseline gap-2 border-b border-border-subtle px-3 py-1.5 last:border-b-0">
      <span className="truncate font-medium">{work.title}</span>
      <span className="ml-auto shrink-0 text-ink-muted">
        {work.media_type}
        {work.year ? ` · ${work.year}` : ""}
      </span>
    </li>
  );
}
