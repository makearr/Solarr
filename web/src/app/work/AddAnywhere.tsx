import { useState } from "react";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useQueries, type UseQueryResult } from "@tanstack/react-query";
import { Search } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { useLibraries } from "@/lib/resources";
import { useMediaTypes, type MediaTypeInfo } from "@/lib/useMediaTypes";
import { Button } from "@/ui/Button";
import { EmptyState } from "@/ui/EmptyState";
import { inputStyle } from "@/ui/form";

import { useDebouncedValue } from "../useDebouncedValue";
import { CandidateCard, LOOKUP_PAGE, MAXIMUM_LOOKUP, MINIMUM_QUERY_LENGTH } from "./AddWork";

type LibraryEntry = components["schemas"]["Library"];

/** One block of results: a media type's kind, looked up through the first of its libraries in scope. */
interface Target {
  info: MediaTypeInfo;
  kind: string;
  kindLabel: string;
  libraries: LibraryEntry[];
}

/** The dashboard's Add to library, Seerr's search: every library at once or one, grouped by what it adds. */
export function AddAnywhere() {
  const navigate = useNavigate();
  const opened = useSearch({ strict: false }) as { q?: string; library?: string };
  const libraries = useLibraries().data ?? [];
  const mediaTypes = useMediaTypes();
  const [query, setQuery] = useState(opened.q ?? "");
  const [limit, setLimit] = useState(LOOKUP_PAGE);
  const debounced = useDebouncedValue(query.trim(), 300);
  const scope = libraries.filter((library) => !opened.library || String(library.id) === opened.library);

  const targets: Target[] = [];
  for (const info of mediaTypes) {
    const ofType = scope.filter((library) => library.media_type === info.key);
    if (ofType.length === 0) continue;
    for (const kind of info.add_kinds) targets.push({ info, kind: kind.key, kindLabel: kind.label, libraries: ofType });
  }

  const remember = (next: { q?: string; library?: string }) =>
    void navigate({ to: "/add", search: { q: next.q || undefined, library: next.library || undefined }, replace: true });

  const searchable = debounced.length >= MINIMUM_QUERY_LENGTH;
  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-4 overflow-y-auto p-4">
      <h1 className="text-base font-semibold">Add to library</h1>
      <div className="flex flex-wrap gap-2">
        <label className="relative flex min-w-64 flex-1 items-center">
          <Search aria-hidden className="pointer-events-none absolute left-3 size-4 text-ink-muted" />
          <input
            autoFocus
            type="search"
            value={query}
            onChange={(event) => {
              setQuery(event.target.value);
              setLimit(LOOKUP_PAGE);
              remember({ q: event.target.value.trim(), library: opened.library });
            }}
            placeholder="Search for something to add…"
            aria-label="Search for something to add"
            className="w-full rounded-md border border-border-subtle bg-surface py-2 pl-9 pr-3 text-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          />
        </label>
        <select
          aria-label="Search in"
          value={opened.library ?? ""}
          onChange={(event) => {
            setLimit(LOOKUP_PAGE);
            remember({ q: query.trim(), library: event.target.value });
          }}
          className={inputStyle}
        >
          <option value="">All libraries</option>
          {libraries.map((library) => (
            <option key={library.id} value={String(library.id)}>
              {library.name}
            </option>
          ))}
        </select>
      </div>
      {query.trim().length > 0 && debounced.length < MINIMUM_QUERY_LENGTH && <p className="text-sm text-ink-muted">Keep typing: at least two characters.</p>}
      {searchable && targets.length === 0 && <EmptyState title="No library to add to yet." />}
      {searchable && (
        <TargetResults key={`${opened.library ?? ""}:${debounced}`} targets={targets} query={debounced} limit={limit} onMore={() => setLimit(limit + LOOKUP_PAGE)} />
      )}
    </div>
  );
}

type Candidate = components["schemas"]["AddCandidate"];

function TargetResults({ targets, query, limit, onMore }: { targets: Target[]; query: string; limit: number; onMore: () => void }) {
  const results = useQueries({
    queries: targets.map((target) => ({
      queryKey: ["work-lookup", target.libraries[0]!.id, target.kind, query, limit],
      placeholderData: (previous: Candidate[] | undefined) => previous,
      queryFn: () => ok(api.GET("/works/lookup", {
        params: { query: { category_id: target.libraries[0]!.id, q: query, kind: target.kind, limit } },
      })),
    })),
  });
  // Another page may exist while any block came back as long as it was asked for.
  const more = limit < MAXIMUM_LOOKUP && results.some((result) => (result.data?.length ?? 0) >= limit);
  return (
    <>
      {targets.map((target, index) => (
        <TargetBlock key={`${target.info.key}:${target.kind}`} target={target} showHeading={targets.length > 1} result={results[index]!} />
      ))}
      {more && (
        <div className="flex justify-center">
          <Button busy={results.some((result) => result.isFetching)} busyLabel="Searching…" onClick={onMore}>
            More
          </Button>
        </div>
      )}
    </>
  );
}

function TargetBlock({
  target,
  showHeading,
  result,
}: {
  target: Target;
  showHeading: boolean;
  result: UseQueryResult<Candidate[]>;
}) {
  const [open, setOpen] = useState<string>();
  const lookupLibrary = target.libraries[0]!;
  const heading = `${target.libraries.length === 1 ? lookupLibrary.name : target.info.label} · ${target.kindLabel}`;
  return (
    <section className="flex flex-col gap-2">
      {showHeading && <h2 className="text-xs font-semibold uppercase tracking-wide text-ink-muted">{heading}</h2>}
      {result.isFetching && !result.data && <p className="text-sm text-ink-muted">Searching…</p>}
      {result.isError && (
        <p className="text-sm text-danger">{(result.error as { detail?: string }).detail ?? "The metadata sources could not be reached. Try again in a moment."}</p>
      )}
      {result.data?.length === 0 && !result.isFetching && <p className="text-sm text-ink-muted">Nothing found.</p>}
      <ul className="flex flex-col gap-3">
        {(result.data ?? []).map((candidate) => {
          const key = `${candidate.provider}:${candidate.external_id}`;
          return (
            <CandidateCard
              key={key}
              candidate={candidate}
              categoryId={String(lookupLibrary.id)}
              libraries={target.libraries}
              info={target.info}
              open={open === key}
              onToggle={() => setOpen(open === key ? undefined : key)}
            />
          );
        })}
      </ul>
    </section>
  );
}
