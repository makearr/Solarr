import { useState } from "react";
import { Link, useParams, useSearch } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Plus, Search } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t, vocabularyOf } from "@/lib/i18n";
import { remoteImage } from "@/lib/remoteImage";
import { useLibraries, useQualityProfiles } from "@/lib/resources";
import { useLibrary } from "@/lib/useLibrary";
import { useMediaTypes, type MediaTypeInfo } from "@/lib/useMediaTypes";
import { usePreference } from "@/lib/usePreference";
import { Button } from "@/ui/Button";
import { Chip } from "@/ui/Chip";
import { EmptyState } from "@/ui/EmptyState";
import { Image } from "@/ui/Image";

import { useDebouncedValue } from "../useDebouncedValue";
import { IncompleteDiscographies } from "./IncompleteDiscographies";

type Candidate = components["schemas"]["AddCandidate"];
type LibraryEntry = components["schemas"]["Library"];

// Mirrors library.ErrQueryTooShort's floor (section 21.5).
export const MINIMUM_QUERY_LENGTH = 2;
// What a lookup asks for at first and what each More adds, up to the API's ceiling.
export const LOOKUP_PAGE = 20;
export const MAXIMUM_LOOKUP = 100;

/** Adding to a library (ADR-56's per-library half), Sonarr's and Lidarr's Add New: look the type's sources up for one
 *  of the things it adds, and add one with its monitoring, profile and a search. */
export function AddWork() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const opened = useSearch({ strict: false }) as { q?: string; kind?: string };
  const library = useLibrary(categoryId);
  const mediaTypes = useMediaTypes();
  const info = mediaTypes.find((entry) => entry.key === library?.media_type);
  // Every media type of a mixed library, so Add offers series and movies alike (L4).
  const head = library?.library_id ?? library?.id;
  const siblings = (useLibraries().data ?? []).filter((entry) => entry.id === head || entry.library_id === head);
  const kinds = info?.add_kinds ?? [];
  const [query, setQuery] = useState(opened.q ?? "");
  const [chosenKind, setKind] = useState(opened.kind);
  const kind = kinds.find((entry) => entry.key === chosenKind)?.key ?? kinds[0]?.key;
  const [open, setOpen] = useState<string>();
  const [limit, setLimit] = useState(LOOKUP_PAGE);
  const debounced = useDebouncedValue(query.trim(), 300);
  const searchable = debounced.length >= MINIMUM_QUERY_LENGTH && kind !== undefined;

  const results = useQuery({
    queryKey: ["work-lookup", Number(categoryId), kind, debounced, limit],
    enabled: searchable,
    placeholderData: (previous) => previous,
    queryFn: () => ok(api.GET("/works/lookup", {
      params: { query: { category_id: Number(categoryId), q: debounced, kind, limit } },
    })),
  });

  if (!library || !info) return null;
  const kindLabel = kinds.find((entry) => entry.key === kind)?.label.toLowerCase() ?? "";
  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-4 overflow-y-auto p-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-base font-semibold">Add to {library.name}</h1>
        {siblings.length > 1 && (
          <div className="flex gap-1" role="group" aria-label="Type">
            {siblings.map((entry) => (
              <Link
                key={entry.id}
                to="/library/$categoryId/discover"
                params={{ categoryId: String(entry.id) }}
                search={{ q: query || undefined }}
                aria-current={entry.id === library.id ? "page" : undefined}
                className={`rounded-md border px-3 py-1.5 text-sm ${entry.id === library.id ? "border-accent bg-accent text-accent-ink" : "border-border-subtle text-ink-muted hover:text-ink"}`}
              >
                {mediaTypes.find((type) => type.key === entry.media_type)?.work.many ?? entry.media_type}
              </Link>
            ))}
          </div>
        )}
        {kinds.length > 1 && (
          <div className="ml-auto flex gap-1" role="group" aria-label="What to look for">
            {kinds.map((entry) => (
              <Button key={entry.key} variant={entry.key === kind ? "primary" : "secondary"} aria-pressed={entry.key === kind} onClick={() => { setKind(entry.key); setLimit(LOOKUP_PAGE); }}>
                {entry.label}
              </Button>
            ))}
          </div>
        )}
      </div>
      <label className="relative flex items-center">
        <Search aria-hidden className="pointer-events-none absolute left-3 size-4 text-ink-muted" />
        <input
          autoFocus
          type="search"
          value={query}
          onChange={(event) => {
            setQuery(event.target.value);
            setLimit(LOOKUP_PAGE);
          }}
          placeholder={`Search for ${kindLabel || "something to add"}…`}
          aria-label={`Search for ${kindLabel}`}
          className="w-full rounded-md border border-border-subtle bg-surface py-2 pl-9 pr-3 text-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        />
      </label>
      {query.trim().length === 0 && info.roles.some((role) => role.lead) && <IncompleteDiscographies categoryId={Number(categoryId)} />}
      {query.trim().length > 0 && debounced.length < MINIMUM_QUERY_LENGTH && <p className="text-sm text-ink-muted">Keep typing: at least two characters.</p>}
      {searchable && results.isFetching && !results.data && <p className="text-sm text-ink-muted">Searching…</p>}
      {searchable && results.isError && (
        <p className="text-sm text-danger">{(results.error as { detail?: string }).detail ?? "The metadata sources could not be reached. Try again in a moment."}</p>
      )}
      {searchable && results.data?.length === 0 && !results.isFetching && <EmptyState title={`Nothing found for “${debounced}”.`} />}
      <ul className="flex flex-col gap-3">
        {(results.data ?? []).map((candidate) => {
          const key = `${candidate.provider}:${candidate.external_id}`;
          return (
            <CandidateCard
              key={key}
              candidate={candidate}
              categoryId={categoryId}
              info={info}
              open={open === key}
              onToggle={() => setOpen(open === key ? undefined : key)}
            />
          );
        })}
      </ul>
      {searchable && limit < MAXIMUM_LOOKUP && (results.data?.length ?? 0) >= limit && (
        <div className="flex justify-center">
          <Button busy={results.isFetching} busyLabel="Searching…" onClick={() => setLimit(limit + LOOKUP_PAGE)}>
            More
          </Button>
        </div>
      )}
    </div>
  );
}

/** One found candidate and its Add panel; `libraries` offers a choice where several hold its type. */
export function CandidateCard({
  candidate,
  categoryId,
  libraries,
  info,
  open,
  onToggle,
}: {
  candidate: Candidate;
  categoryId: string;
  libraries?: readonly LibraryEntry[];
  info: MediaTypeInfo;
  open: boolean;
  onToggle: () => void;
}) {
  // A type's first add kind is its works; another (music's artists) adds a person to follow.
  const person = candidate.kind !== info.add_kinds[0]?.key;
  const [added, setAdded] = useState<components["schemas"]["AddWorkResult"]>();
  const held = candidate.library_id ?? added?.work_id ?? added?.person_id;
  const heldLink =
    person
      ? ({ to: "/library/$categoryId/people/$personId", params: { categoryId, personId: String(held) } } as const)
      : ({ to: "/library/$categoryId/works/$workId", params: { categoryId, workId: String(held) } } as const);
  return (
    <li className="rounded-lg border border-border-subtle bg-surface p-3">
      <div className="flex gap-3">
        {candidate.poster_url && (
          <div className="w-[75px] shrink-0">
            <Image shape="poster" title={candidate.title} src={remoteImage(candidate.poster_url, "thumb")} />
          </div>
        )}
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div className="flex flex-wrap items-baseline gap-2">
            <span className="text-sm font-semibold">{candidate.title}</span>
            <span className="text-xs text-ink-muted">{[candidate.subtitle, candidate.year, candidate.status].filter(Boolean).join(" · ")}</span>
            {candidate.provider === "tmdb" && <Chip title="TheTVDB does not list it; its episodes come from TMDB">TMDB only</Chip>}
          </div>
          {candidate.overview && <p className="line-clamp-3 text-xs text-ink-muted">{candidate.overview}</p>}
          <div className="mt-auto">
            {held !== undefined ? (
              <Link {...heldLink} className="inline-flex items-center gap-1 text-xs text-ok">
                <Check aria-hidden className="size-3.5" /> {added ? "Added: open it" : "Already in your library"}
              </Link>
            ) : (
              <Button icon={open ? undefined : <Plus aria-hidden className="size-3.5" />} onClick={onToggle}>
                {open ? t("action.cancel") : "Add…"}
              </Button>
            )}
          </div>
        </div>
      </div>
      {open && held === undefined && (
        <AddPanel candidate={candidate} categoryId={categoryId} libraries={libraries} info={info} person={person} onAdded={setAdded} />
      )}
    </li>
  );
}

/** The add dialog's choices: monitoring (a monitor option where the type has them), the quality profile, the search. */
function AddPanel({
  candidate,
  categoryId: firstCategoryId,
  libraries,
  info,
  person,
  onAdded,
}: {
  candidate: Candidate;
  categoryId: string;
  libraries?: readonly LibraryEntry[];
  info: MediaTypeInfo;
  person: boolean;
  onAdded: (result: components["schemas"]["AddWorkResult"]) => void;
}) {
  const queryClient = useQueryClient();
  const [categoryId, setCategoryId] = useState(firstCategoryId);
  const words = vocabularyOf(info);
  const options = info.monitor_options;
  const [monitored, setMonitored] = useState(true);
  const [option, setOption] = usePreference<string>(`add.monitor.${info.key}`, options[0]?.key ?? "");
  const [specials, setSpecials] = useState(false);
  const [profileId, setProfileId] = useState(0);
  // Lidarr's and Sonarr's "Start search for missing", remembered per person (D13).
  const [search, setSearch] = usePreference("discover.search_on_add", true);
  const profiles = useQualityProfiles();
  const add = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/works", {
        body: {
          category_id: Number(categoryId), kind: candidate.kind, provider: candidate.provider, external_id: candidate.external_id,
          monitored: options.length > 0 ? option !== "none" : monitored, monitor_option: options.length > 0 ? option : undefined,
          specials, quality_profile_id: profileId || undefined, search,
        },
      });
      if (error) throw error;
      // A type that does not search on its own is searched as a whole work, season by season for a series.
      if (search && !data.searching && data.work_id) {
        void api.POST("/works/{id}/grab", { params: { path: { id: data.work_id } } });
      }
      return data;
    },
    onSuccess: (data) => {
      void queryClient.invalidateQueries({ queryKey: ["works"] });
      void queryClient.invalidateQueries({ queryKey: ["libraries"] });
      void queryClient.invalidateQueries({ queryKey: ["people"] });
      onAdded(data);
    },
  });
  const field = "rounded-md border border-border-subtle bg-canvas px-2 py-1 text-sm";
  return (
    <div className="mt-3 flex flex-col gap-2 rounded-md border border-border-subtle bg-canvas p-3 text-sm">
      {libraries && libraries.length > 1 && (
        <label className="flex flex-wrap items-center gap-2">
          <span className="w-24 text-xs text-ink-muted">Library</span>
          <select value={categoryId} onChange={(event) => setCategoryId(event.target.value)} className={field}>
            {libraries.map((library) => (
              <option key={library.id} value={String(library.id)}>
                {library.name}
              </option>
            ))}
          </select>
        </label>
      )}
      {options.length > 0 ? (
        <label className="flex flex-wrap items-center gap-2">
          <span className="w-24 text-xs text-ink-muted">Monitor</span>
          <select value={option} onChange={(event) => setOption(event.target.value)} className={field}>
            {options.map((entry) => (
              <option key={entry.key} value={entry.key}>
                {entry.label}
              </option>
            ))}
          </select>
          <label className="flex items-center gap-1 text-xs text-ink-muted">
            <input type="checkbox" checked={specials} onChange={(event) => setSpecials(event.target.checked)} /> also the specials
          </label>
        </label>
      ) : (
        <label className="flex items-center gap-2 text-xs text-ink-muted">
          <span className="w-24" />
          <input type="checkbox" checked={monitored} onChange={(event) => setMonitored(event.target.checked)} />
          {person ? `Monitor: add their ${words.works} and follow new ones` : t("action.monitor")}
        </label>
      )}
      {!person && (
        <label className="flex flex-wrap items-center gap-2">
          <span className="w-24 text-xs text-ink-muted">Quality profile</span>
          <select value={profileId} onChange={(event) => setProfileId(Number(event.target.value))} className={field}>
            <option value={0}>The library&apos;s default</option>
            {(profiles.data ?? [])
              .filter((profile) => profile.media_type === info.key)
              .map((profile) => (
                <option key={profile.id} value={profile.id}>
                  {profile.name}
                </option>
              ))}
          </select>
        </label>
      )}
      <label className="flex items-center gap-2 text-xs text-ink-muted">
        <span className="w-24" />
        <input type="checkbox" checked={search} onChange={(event) => setSearch(event.target.checked)} /> Start search for missing {words.parts === "parts" ? words.works : words.parts}
      </label>
      <div className="flex items-center gap-3">
        <span className="w-24" />
        <Button variant="primary" busy={add.isPending} busyLabel="Adding…" onClick={() => add.mutate()}>
          Add {candidate.title}
        </Button>
        {add.isError && <span className="text-xs text-danger">{(add.error as { detail?: string }).detail ?? "It could not be added. Try again."}</span>}
      </div>
    </div>
  );
}

/** The add panel for a candidate found elsewhere (a person's "also known for"), in the library it would go to. */
export function AddToLibrary({
  candidate,
  categoryId,
  onDone,
}: {
  candidate: Pick<Candidate, "provider" | "external_id" | "title"> & { kind?: string };
  categoryId: string;
  onDone: () => void;
}) {
  const library = useLibrary(categoryId);
  const info = useMediaTypes().find((entry) => entry.key === library?.media_type);
  if (!info) return null;
  return (
    <AddPanel
      candidate={{ kind: candidate.kind ?? info.add_kinds[0]?.key ?? "", ...candidate }}
      categoryId={categoryId}
      info={info}
      person={false}
      onAdded={onDone}
    />
  );
}
