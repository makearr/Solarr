import { useState } from "react";

import { useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Search } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

import { useDebouncedValue } from "./useDebouncedValue";

type DiscoverResult = components["schemas"]["DiscoverResult"];
type DiscoverKind = components["schemas"]["DiscoverKind"];

// Mirrors library.ErrQueryTooShort's floor (section 21.5, SearchBox's own constant).
const MINIMUM_QUERY_LENGTH = 2;
const DEBOUNCE_MS = 250;

/** ADR-56's per-library half: search this category's own metadata provider for
 *  something not in the library yet, and add it as intent (a resolved work
 *  plus a monitored desired_item -- never a download). The global half
 *  (search across every media type, with a suggested destination) is not
 *  built; this screen is always scoped to the library it was opened from. */
export function Discover() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const [query, setQuery] = useState("");
  const [kind, setKind] = useState<DiscoverKind>("album");
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

  const add = useMutation({
    mutationFn: async (result: DiscoverResult) => {
      const { data, error } = await api.POST("/discover/add", {
        body: {
          category_id: Number(categoryId),
          provider: result.provider,
          external_id: result.external_id,
          // openapi-typescript makes a field with a `default` non-optional
          // even when not `required` (CLAUDE.md's own documented gotcha).
          monitored: true,
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
                    {result.kind === "artist" ? result.disambiguation : result.artist}
                    {result.year != null && ` · ${result.year}`}
                  </div>
                </div>
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
              </li>
            );
          })}
        </ul>
      )}

      {add.isSuccess && add.data?.kind === "artist" && (
        <p className="text-xs text-ink-muted">
          Artist added and monitored. Their albums appear as the subscription expands, which runs on its
          own schedule rather than immediately.
        </p>
      )}
      {add.isSuccess && add.data?.kind === "album" && (
        <p className="text-xs text-ink-muted">
          Added with priority {add.data.priority}. There is no acquisition queue yet (stage 5) to show a
          position within — the score alone is what will decide it once one exists.
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

function resultKey(result: DiscoverResult): string {
  return `${result.provider}:${result.external_id}`;
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
