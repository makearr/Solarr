import { useState } from "react";

import { useNavigate, useParams, Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { useIsAdministrator } from "@/lib/useAdministrator";
import type { components } from "@/api/schema";
import { albumStatusLabel, albumStatusText } from "@/lib/albumStatus";
import { discCountOf, formatTrackNumber } from "@/lib/trackNumber";

import { CoverArt } from "./CoverArt";

type PersonWork = components["schemas"]["PersonWork"];
type PreviewReleaseGroup = components["schemas"]["PreviewReleaseGroup"];

// Real MusicBrainz release-group types, in the order Lidarr's own artist
// page groups them; anything else this library has never seen a value for
// (a future MusicBrainz type, or no primary_type at all yet) falls through
// to TYPE_LABELS' own default below rather than being silently dropped.
const TYPE_ORDER = ["Album", "EP", "Single", "Broadcast", "Other"];
const TYPE_LABELS: Record<string, string> = {
  Album: "Albums", EP: "EPs", Single: "Singles", Broadcast: "Broadcasts", Other: "Other",
};
const UNCLASSIFIED_LABEL = "Not yet classified";

interface DiscographyGroup {
  label: string;
  works: PersonWork[];
}

// Groups preserve the discography's own order within themselves (already
// year, then sort_title, then id from the API) -- this only partitions,
// it never re-sorts. A work with no primary_type yet (created before
// migration 0028, or from a source with no MusicBrainz answer) lands in its
// own trailing group rather than being guessed into "Other".
function groupDiscography(discography: readonly PersonWork[]): DiscographyGroup[] {
  const byType = new Map<string, PersonWork[]>();
  for (const work of discography) {
    const key = work.primary_type ?? "";
    const list = byType.get(key);
    if (list) list.push(work);
    else byType.set(key, [work]);
  }

  const groups: DiscographyGroup[] = [];
  for (const type of TYPE_ORDER) {
    const works = byType.get(type);
    if (works) {
      groups.push({ label: TYPE_LABELS[type] ?? type, works });
      byType.delete(type);
    }
  }
  const unclassified = byType.get("");
  byType.delete("");
  for (const [type, works] of byType) {
    groups.push({ label: type, works });
  }
  if (unclassified) groups.push({ label: UNCLASSIFIED_LABEL, works: unclassified });
  return groups;
}

// The artist page (ADR-32): picture, genre chips (the union of every genre
// tagged on any work in the discography -- Person carries no genre of its
// own), biography, and the discography grouped Lidarr-style by release type,
// each row expandable in place to its own tracklist rather than always
// navigating away. No coverage (held/missing/upgradable) yet -- that needs
// desired_item for people, which stage 4 has not built.
export function PersonDetail() {
  const { id: rawID } = useParams({ strict: false }) as { id?: string };
  const id = rawID !== undefined ? Number(rawID) : undefined;
  const queryClient = useQueryClient();

  const detail = useQuery({
    queryKey: ["person", id],
    enabled: id !== undefined,
    queryFn: async () => {
      const { data, error } = await api.GET("/people/{id}", {
        params: { path: { id: id as number } },
      });
      if (error) throw error;
      return data;
    },
  });

  const [subscribing, setSubscribing] = useState(false);
  const navigate = useNavigate();

  const subscribe = useMutation({
    mutationFn: async (excludeSecondaryTypes: string[] | undefined) => {
      if (id === undefined) return;
      const { error } = await api.POST("/people/{id}/subscribe", {
        params: { path: { id } },
        body: { media_types: ["music"], exclude_secondary_types: excludeSecondaryTypes },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      setSubscribing(false);
      void queryClient.invalidateQueries({ queryKey: ["person", id] });
      void queryClient.invalidateQueries({ queryKey: ["people"] });
    },
  });

  const searchMissing = useMutation({
    mutationFn: async () => {
      if (id === undefined) return undefined;
      const { data, error } = await api.POST("/people/{id}/search", { params: { path: { id } } });
      if (error) throw error;
      return data;
    },
  });

  const monitorAll = useMutation({
    mutationFn: async () => {
      if (id === undefined) return undefined;
      const { data, error } = await api.POST("/people/{id}/monitor-all", { params: { path: { id } } });
      if (error) throw error;
      return data;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["person", id] }),
  });
  const administrator = useIsAdministrator();

  const remove = useMutation({
    mutationFn: async () => {
      if (id === undefined) return;
      const { error } = await api.POST("/people/{id}/remove", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["people"] });
      void navigate({ to: "/people" });
    },
  });

  if (id === undefined) return <Message>No person id in the URL.</Message>;
  if (detail.isLoading) return <Message>Loading…</Message>;
  if (detail.error !== null || !detail.data) {
    return <Message>This person could not be read.</Message>;
  }

  const person = detail.data;
  const facts = [
    person.disambiguation,
    person.country,
    [person.born, person.died].filter(Boolean).join(" – "),
  ].filter((fact): fact is string => Boolean(fact));
  // ADR-54's own safety argument for retiring the top-level People page:
  // arriving at a person through one library (Music -> Artists) must not
  // hide their credits in another. Not yet a link into a per-media-type
  // browse view -- there is no such surface to link into today.
  const mediaTypes = [...new Set(person.discography.map((work) => work.media_type))];
  const groups = groupDiscography(person.discography);

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-3xl flex-col gap-6 p-6">
        <header className="flex items-start gap-4">
          <CoverArt
            subject="people"
            workId={person.id}
            title={person.name}
            size="full"
            className="h-40 w-40 shrink-0 rounded-full object-cover"
          />
          <div className="flex flex-1 items-start justify-between gap-4 pt-1">
            <div className="flex flex-col gap-1.5">
              <h1 className="text-lg font-semibold">{person.name}</h1>
              {facts.length > 0 && <p className="text-sm text-ink-muted">{facts.join(" · ")}</p>}
              {person.aliases.length > 0 && (
                <p className="text-xs text-ink-muted">
                  Also known as {person.aliases.join(", ")}
                </p>
              )}
              {person.genres.length > 0 && (
                <div className="flex flex-wrap gap-1.5 pt-1">
                  {person.genres.map((genre) => (
                    <span
                      key={genre}
                      className="rounded-full border border-border-subtle px-2 py-0.5 text-xs text-ink-muted"
                    >
                      {genre}
                    </span>
                  ))}
                </div>
              )}
            </div>
            {!person.monitored && (
              <button
                type="button"
                disabled={subscribing}
                onClick={() => setSubscribing(true)}
                className="shrink-0 rounded-md border border-border-subtle px-3 py-1.5 text-sm
                           enabled:hover:bg-surface disabled:cursor-default disabled:text-ink-muted"
              >
                Monitor
              </button>
            )}
            {person.monitored && (
              <span className="shrink-0 rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted">
                Monitored
              </span>
            )}
            <button
              type="button"
              disabled={remove.isPending}
              onClick={() => {
                if (
                  window.confirm(
                    `Stop following ${person.name} and remove them from the library?\n\n` +
                      "Their albums and any files stay exactly where they are — this only removes the artist.",
                  )
                ) {
                  remove.mutate();
                }
              }}
              className="shrink-0 rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted
                         enabled:hover:text-danger disabled:cursor-default"
            >
              Remove
            </button>
          </div>
        </header>

        <div className="flex flex-wrap items-center gap-2 text-xs text-ink-muted">
          <button
            type="button"
            disabled={searchMissing.isPending}
            onClick={() => searchMissing.mutate()}
            className="flex items-center gap-1 rounded-md border border-border-subtle px-2 py-1 hover:text-ink disabled:opacity-50"
          >
            Search all missing
          </button>
          {administrator && (
            <button
              type="button"
              disabled={monitorAll.isPending}
              onClick={() => {
                if (window.confirm(`Monitor every album of ${person.name}?`)) monitorAll.mutate();
              }}
              className="rounded-md border border-border-subtle px-2 py-1 hover:text-ink disabled:opacity-50"
            >
              Monitor all
            </button>
          )}
          {searchMissing.isSuccess && (
            <span>
              {searchMissing.data?.duplicate ? "A search for this artist is already running." : "Searching in the background."}
            </span>
          )}
          {searchMissing.isError && <span className="text-danger">The search could not start.</span>}
          {monitorAll.isSuccess && (
            <span>
              {monitorAll.data?.albums === 1 ? "1 album" : `${monitorAll.data?.albums ?? 0} albums`} monitored.
            </span>
          )}
          {monitorAll.isError && <span className="text-danger">The albums could not be monitored.</span>}
        </div>

        {subscribing && id !== undefined && (
          <SubscribePanel
            personId={id}
            pending={subscribe.isPending}
            error={subscribe.isError}
            onCancel={() => setSubscribing(false)}
            onConfirm={(excludeSecondaryTypes) => subscribe.mutate(excludeSecondaryTypes)}
          />
        )}

        {person.biography && (
          <Section title="Biography">
            <p className="text-sm leading-relaxed">{person.biography}</p>
          </Section>
        )}

        {mediaTypes.length > 1 && (
          <Section title="Also credited in">
            <div className="flex flex-wrap gap-1.5">
              {mediaTypes.map((mediaType) => (
                <span
                  key={mediaType}
                  className="rounded-full border border-border-subtle px-2 py-0.5 text-xs
                             text-ink-muted"
                >
                  {mediaType}
                </span>
              ))}
            </div>
          </Section>
        )}

        {groups.map((group) => (
          <Section key={group.label} title={`${group.label} (${group.works.length})`}>
            <ul
              className="flex flex-col divide-y divide-border-subtle rounded-lg border
                         border-border-subtle bg-surface"
            >
              {group.works.map((work) => (
                <AlbumRow key={work.id} work={work} />
              ))}
            </ul>
          </Section>
        ))}
      </div>
    </div>
  );
}

// A release group is excluded if any of its own secondary types matches an
// excluded one, case-insensitive substring -- mirrors music.
// SubscriptionFilters.Admits's own rule exactly, so the live preview here
// never disagrees with what the real subscription will do once saved. Low
// duplication risk: a 2-line predicate, not real business logic, and the
// authoritative decision still runs server-side at expand time regardless.
// excluded is already lowercased (the checkbox state's own canonical form).
function admits(group: PreviewReleaseGroup, excludedLower: ReadonlySet<string>): boolean {
  return !group.secondary_types.some((secondary) =>
    [...excludedLower].some((exclusion) => secondary.toLowerCase().includes(exclusion)),
  );
}

// The subscribe screen's own preview-before-enable panel (ADR-38, todo 4.4):
// fetched once when opened, filter checkboxes recomputed entirely client-side
// against that one fetch so toggling one costs no round trip. Confirming
// sends exactly the exclusion set the user saw, never a hidden default.
function SubscribePanel({
  personId,
  pending,
  error,
  onCancel,
  onConfirm,
}: {
  personId: number;
  pending: boolean;
  error: boolean;
  onCancel: () => void;
  onConfirm: (excludeSecondaryTypes: string[] | undefined) => void;
}) {
  const preview = useQuery({
    queryKey: ["subscription-preview", personId],
    queryFn: async () => {
      const { data, error } = await api.GET("/people/{id}/subscription-preview", {
        params: { path: { id: personId } },
      });
      if (error) throw error;
      return data;
    },
  });

  // Keyed lowercase throughout (state, matching, and the checkbox map below)
  // -- real MusicBrainz secondary_types arrive Title Case ("Compilation")
  // while DefaultExcludedSecondaryTypes ships lowercase ("compilation").
  // Case-sensitive keys produced two checkboxes for the same real type,
  // found live verifying against the real library (Lil Wayne's own
  // discography: "compilation" and "Compilation" both listed, only one of
  // which the default-checked state actually matched).
  const [excludedLower, setExcludedLower] = useState<Set<string> | null>(null);

  // Seeded once the preview answers: the default exclusions, checked, plus
  // every other secondary type this artist's own discography actually has,
  // unchecked -- there is no point offering a box for a type nothing here
  // carries, except the defaults themselves (a future release could).
  const seeded = excludedLower !== null;
  if (preview.data && !seeded) {
    setExcludedLower(new Set(preview.data.default_excluded_secondary_types.map((type) => type.toLowerCase())));
  }

  // "conflict" is this endpoint's only 409 cause (no MusicBrainz artist id
  // yet to browse a discography for) -- the stable part of the RFC 7807
  // contract client.ts's own ProblemType documents callers branching on.
  const noArtistID = preview.error !== null && (preview.error as { type?: string }).type === "conflict";

  if (preview.isLoading) {
    return (
      <section className="rounded-lg border border-border-subtle bg-surface p-3 text-sm text-ink-muted">
        Loading this artist's discography…
      </section>
    );
  }

  if (noArtistID) {
    return (
      <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3 text-sm">
        <p className="text-ink-muted">
          No MusicBrainz artist id yet, so there is nothing to preview -- monitoring will use the
          default filters once one is resolved.
        </p>
        <PanelButtons pending={pending} onCancel={onCancel} onConfirm={() => onConfirm(undefined)} />
      </section>
    );
  }

  if (preview.error !== null || !preview.data) {
    return (
      <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3 text-sm">
        <p className="text-danger">This artist's discography could not be read.</p>
        <PanelButtons pending={pending} onCancel={onCancel} onConfirm={() => onConfirm(undefined)} />
      </section>
    );
  }

  const groups = preview.data.release_groups;

  // One entry per distinct type, case-insensitive -- labelled with whichever
  // casing is more likely to read naturally: the real MusicBrainz value
  // when this artist actually has one, the shipped default's own casing
  // otherwise.
  const labelByLower = new Map<string, string>();
  for (const type of preview.data.default_excluded_secondary_types) {
    labelByLower.set(type.toLowerCase(), type);
  }
  for (const type of groups.flatMap((group) => group.secondary_types)) {
    labelByLower.set(type.toLowerCase(), type);
  }
  const availableTypes = [...labelByLower.keys()].sort((a, b) => a.localeCompare(b));

  const activeExcluded =
    excludedLower ?? new Set(preview.data.default_excluded_secondary_types.map((type) => type.toLowerCase()));
  const admittedCount = groups.filter((group) => admits(group, activeExcluded)).length;

  return (
    <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3 text-sm">
      <p className="text-ink-muted">
        {admittedCount} of {groups.length} release{groups.length === 1 ? "" : "s"} will be
        monitored{activeExcluded.size > 0 && ` (${activeExcluded.size} type${activeExcluded.size === 1 ? "" : "s"} excluded)`}.
      </p>

      {availableTypes.length > 0 && (
        <fieldset className="flex flex-wrap gap-x-4 gap-y-1.5">
          <legend className="mb-1 text-xs text-ink-muted">Exclude by type</legend>
          {availableTypes.map((typeLower) => (
            <label key={typeLower} className="flex items-center gap-1.5 text-xs text-ink-muted">
              <input
                type="checkbox"
                checked={activeExcluded.has(typeLower)}
                onChange={(event) => {
                  const next = new Set(activeExcluded);
                  if (event.target.checked) next.add(typeLower);
                  else next.delete(typeLower);
                  setExcludedLower(next);
                }}
                className="size-3.5"
              />
              {labelByLower.get(typeLower)}
            </label>
          ))}
        </fieldset>
      )}

      {error && <p className="text-xs text-danger">That could not be saved.</p>}
      <PanelButtons
        pending={pending}
        onCancel={onCancel}
        onConfirm={() => onConfirm([...activeExcluded])}
      />
    </section>
  );
}

function PanelButtons({
  pending,
  onCancel,
  onConfirm,
}: {
  pending: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div className="flex items-center gap-2">
      <button
        type="button"
        disabled={pending}
        onClick={onConfirm}
        className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50"
      >
        {pending ? "Subscribing…" : "Confirm"}
      </button>
      <button
        type="button"
        disabled={pending}
        onClick={onCancel}
        className="rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted
                   enabled:hover:text-ink disabled:opacity-50"
      >
        Cancel
      </button>
    </div>
  );
}

// One discography row, expandable in place to its own tracklist (lazy --
// fetched only once expanded, and only once per row) rather than always
// sending the user to WorkDetail just to see what is on an album.
function AlbumRow({ work }: { work: PersonWork }) {
  const [expanded, setExpanded] = useState(false);

  const tracks = useQuery({
    queryKey: ["work", work.id],
    enabled: expanded,
    queryFn: async () => {
      const { data, error } = await api.GET("/works/{id}", {
        params: { path: { id: work.id } },
      });
      if (error) throw error;
      return data;
    },
  });

  return (
    <li className="flex flex-col text-sm">
      <div className="flex items-center gap-3 px-3 py-2">
        <button
          type="button"
          aria-expanded={expanded}
          aria-label={expanded ? "Collapse tracklist" : "Expand tracklist"}
          onClick={() => setExpanded((current) => !current)}
          className="shrink-0 text-ink-muted hover:text-ink"
        >
          {expanded ? "▾" : "▸"}
        </button>
        <CoverArt
          workId={work.id}
          title={work.title}
          size="thumb"
          className="h-8 w-8 shrink-0 rounded object-cover"
        />
        <Link to="/works/$id" params={{ id: String(work.id) }} className="min-w-0 flex-1 truncate hover:underline">
          {work.title}
        </Link>
        {work.year != null && <span className="shrink-0 text-xs text-ink-muted">{work.year}</span>}
        <AvailabilityBadge work={work} />
      </div>

      {expanded && (
        <div className="border-t border-border-subtle bg-canvas px-3 py-2 pl-11">
          {tracks.isLoading && <p className="text-xs text-ink-muted">Loading tracks…</p>}
          {tracks.error !== null && <p className="text-xs text-ink-muted">Tracks could not be read.</p>}
          {tracks.data && tracks.data.parts.length === 0 && (
            <p className="text-xs text-ink-muted">No tracklist yet.</p>
          )}
          {tracks.data && tracks.data.parts.length > 0 && (
            <ul className="flex flex-col gap-1">
              {tracks.data.parts.map((part) => (
                <li key={part.id} className="flex items-center gap-3 text-xs">
                  <span className="w-10 shrink-0 text-right font-mono tabular-nums text-ink-muted">
                    {formatTrackNumber(part.media_ref, part.sort_key, discCountOf(tracks.data.parts))}
                  </span>
                  <span className="min-w-0 flex-1 truncate">{part.title ?? part.media_ref}</span>
                  {part.runtime_ms != null && (
                    <span className="shrink-0 font-mono text-ink-muted">{formatDuration(part.runtime_ms)}</span>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </li>
  );
}

// Mirrors WorkDetail's own WantedBadge/OnDiskBadge, simplified to what
// PersonWork actually carries (no required/present track counts at this
// level -- that is what expanding the row is for).
function AvailabilityBadge({ work }: { work: PersonWork }) {
  const counts =
    work.required_count != null && work.required_count > 0
      ? `${work.present_count ?? 0}/${work.required_count}`
      : null;
  return (
    <span className="flex shrink-0 items-center gap-2">
      {counts && <span className="font-mono text-xs tabular-nums text-ink-muted">{counts}</span>}
      <span
        className={`rounded-full border border-current px-2 py-0.5 text-xs ${albumStatusText(work.status)}`}
      >
        {albumStatusLabel(work.status)}
      </span>
    </span>
  );
}

function formatDuration(ms: number): string {
  const totalSeconds = Math.round(ms / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">{title}</h2>
      {children}
    </section>
  );
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
