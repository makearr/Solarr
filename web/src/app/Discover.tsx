import { useState } from "react";

import { Link, useParams, useSearch } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Search } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { usePreference } from "@/lib/usePreference";

import { useDebouncedValue } from "./useDebouncedValue";

type DiscoverResult = components["schemas"]["DiscoverResult"];
type DiscoverKind = components["schemas"]["DiscoverKind"];

// Mirrors library.ErrQueryTooShort's floor (section 21.5, SearchBox's own constant).
const MINIMUM_QUERY_LENGTH = 2;
const DEBOUNCE_MS = 250;

/** ADR-56's per-library half: search this category's own metadata provider for
 *  something not in the library yet, and add it as intent (a resolved work
 *  plus a monitored desired_item -- never a download). The global half is the header search's
 *  "Not in your library" rows, which open this screen with their search filled in. */
export function Discover() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  // The header search's "Not in your library" rows open here with their search filled in.
  const opened = useSearch({ strict: false }) as { q?: string; kind?: DiscoverKind };
  const [query, setQuery] = useState(opened.q ?? "");
  const [kind, setKind] = useState<DiscoverKind>(opened.kind === "artist" ? "artist" : "album");
  const [added, setAdded] = useState<Set<string>>(new Set());
  const debounced = useDebouncedValue(query.trim(), DEBOUNCE_MS);
  const searchable = debounced.length >= MINIMUM_QUERY_LENGTH && categoryId !== undefined;
  const queryClient = useQueryClient();

  const results = useQuery({
    queryKey: ["discover-search", categoryId, kind, debounced],
    queryFn: async () => {
      const { data, error } = await api.GET("/discover/search", {
        params: { query: { category_id: Number(categoryId), q: debounced, kind, limit: 20 } },
      });
      if (error) throw error;
      return data;
    },
    enabled: searchable,
  });

  // On by default, and a choice: adding an artist without following them is
  // "have them in the library, do not watch them" (asked for 2026-09-19).
  const [monitor, setMonitor] = useState(true);
  // Lidarr's "Start search for missing albums", on by default and remembered per person (D13).
  const [searchOnAdd, setSearchOnAdd] = usePreference("discover.search_on_add", true);

  const add = useMutation({
    mutationFn: async (result: DiscoverResult) => {
      const { data, error } = await api.POST("/discover/add", {
        body: {
          category_id: Number(categoryId),
          provider: result.provider,
          external_id: result.external_id,
          // openapi-typescript makes a field with a `default` non-optional
          // even when not `required` (CLAUDE.md's own documented gotcha).
          monitored: monitor,
          search: monitor && searchOnAdd,
        },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: (_data, result) => {
      setAdded((current) => new Set(current).add(resultKey(result)));
      void queryClient.invalidateQueries({ queryKey: ["works"] });
      void queryClient.invalidateQueries({ queryKey: ["people"] });
    },
  });

  return (
    <div className="flex h-full flex-col gap-4 p-4">
      <div className="flex items-center gap-3">
        <h2 className="text-sm font-medium">Discover</h2>
        <label className="order-last ml-auto flex items-center gap-1.5 text-xs text-ink-muted">
          <input type="checkbox" checked={monitor} onChange={(e) => setMonitor(e.target.checked)} />
          Monitor what I add
        </label>
        <label className="order-last flex items-center gap-1.5 text-xs text-ink-muted">
          <input
            type="checkbox"
            checked={monitor && searchOnAdd}
            disabled={!monitor}
            onChange={(e) => setSearchOnAdd(e.target.checked)}
          />
          Start search for missing albums
        </label>
        <div className="flex gap-1" role="group" aria-label="What to search for">
          {(["album", "artist"] as const).map((option) => (
            <button
              key={option}
              type="button"
              aria-pressed={kind === option}
              onClick={() => setKind(option)}
              className={`rounded-md border px-2 py-1 text-xs capitalize
                          focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent ${
                            kind === option
                              ? "border-accent bg-accent text-accent-ink"
                              : "border-border-subtle text-ink-muted hover:text-ink"
                          }`}
            >
              {option}s
            </button>
          ))}
        </div>
      </div>

      <div className="relative max-w-md">
        <Search
          aria-hidden
          className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-ink-muted"
        />
        <input
          type="search"
          value={query}
          placeholder={kind === "artist" ? "Search for an artist…" : "Search for an album…"}
          onChange={(event) => setQuery(event.target.value)}
          className="w-full rounded-md border border-border-subtle bg-surface py-1.5 pl-8 pr-3 text-sm
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        />
      </div>

      {!searchable && query.trim().length > 0 && (
        <Message>Keep typing — at least {MINIMUM_QUERY_LENGTH} characters.</Message>
      )}
      {searchable && results.isLoading && <Message>Searching…</Message>}
      {searchable && results.error !== null && (
        <Message>{(results.error as { detail?: string }).detail ?? "That search could not be completed."}</Message>
      )}
      {searchable && results.data && results.data.length === 0 && <Message>No matches.</Message>}

      {searchable && results.data && results.data.length > 0 && (
        <ul className="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto">
          {results.data.map((result) => {
            const key = resultKey(result);
            const isAdded = added.has(key);
            return (
              <li
                key={key}
                className="flex items-center gap-3 rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm"
              >
                <div className="min-w-0 flex-1">
                  <div className="truncate font-medium">{result.title}</div>
                  <div className="truncate text-xs text-ink-muted">
                    {subtitle(result)}
                  </div>
                </div>
                {result.library_id != null ? (
                  // Already here: open it, where monitoring lives, rather than add it twice.
                  <Link
                    {...(result.kind === "artist"
                      ? { to: "/people/$id" as const, params: { id: String(result.library_id) } }
                      : { to: "/works/$id" as const, params: { id: String(result.library_id) } })}
                    className="flex shrink-0 items-center gap-1 rounded-md border border-border-subtle px-2 py-1 text-xs
                               text-ink-muted hover:text-ink"
                  >
                    <Check aria-hidden className="size-3.5" /> In library
                  </Link>
                ) : (
                <button
                  type="button"
                  disabled={isAdded || (add.isPending && add.variables === result)}
                  onClick={() => add.mutate(result)}
                  className="flex shrink-0 items-center gap-1 rounded-md border border-border-subtle px-2 py-1
                             text-xs text-ink-muted hover:text-ink disabled:opacity-50
                             focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
                >
                  {isAdded ? (
                    <>
                      <Check aria-hidden className="size-3.5" /> Added
                    </>
                  ) : (
                    "Add"
                  )}
                </button>
                )}
              </li>
            );
          })}
        </ul>
      )}

      {add.isSuccess && add.data?.kind === "artist" && (
        <p className="text-xs text-ink-muted">
          {!monitor
            ? "Artist added, not monitored. Nothing of theirs will be looked for until you monitor them."
            : searchOnAdd
              ? "Artist added and monitored. Their albums are being added now, then searched for."
              : "Artist added and monitored. Their albums are being added now; missing ones are " +
                "searched for automatically over the next hours."}
        </p>
      )}
      {add.isSuccess && add.data?.kind === "album" && (
        <p className="text-xs text-ink-muted">
          {!monitor
            ? "Album added, not monitored. It will not be looked for until you monitor it."
            : searchOnAdd
              ? "Album added. Searching for it now; Activity → Queue shows the download."
              : "Album added. It will be searched for automatically within the hour."}
        </p>
      )}
      {add.isError && (
        <p className="text-xs text-danger">
          {(add.error as { detail?: string }).detail ?? "That could not be added."}
        </p>
      )}
    </div>
  );
}

/** The separator only earns its place between two present parts -- an artist
 *  with no disambiguation was rendering as "· 1958". */
function subtitle(result: DiscoverResult): string {
  const lead = result.kind === "artist" ? result.disambiguation : result.artist;
  return [lead, result.year?.toString()].filter(Boolean).join(" · ");
}

function resultKey(result: DiscoverResult): string {
  return `${result.provider}:${result.external_id}`;
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
