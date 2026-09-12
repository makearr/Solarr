import { useState } from "react";

import { useParams, Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { CoverArt } from "./CoverArt";

type PersonWork = components["schemas"]["PersonWork"];

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

  const subscribe = useMutation({
    mutationFn: async () => {
      if (id === undefined) return;
      const { error } = await api.POST("/people/{id}/subscribe", {
        params: { path: { id } },
        body: { media_types: ["music"] },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["person", id] });
      void queryClient.invalidateQueries({ queryKey: ["people"] });
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
            <button
              type="button"
              disabled={person.monitored || subscribe.isPending}
              onClick={() => subscribe.mutate()}
              className="shrink-0 rounded-md border border-border-subtle px-3 py-1.5 text-sm
                         enabled:hover:bg-surface disabled:cursor-default disabled:text-ink-muted"
            >
              {person.monitored ? "Monitored" : subscribe.isPending ? "Monitoring…" : "Monitor"}
            </button>
          </div>
        </header>

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
        <AvailabilityBadge onDisk={work.on_disk} wantedState={work.wanted_state} />
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
                  <span className="w-10 shrink-0 font-mono text-ink-muted">{part.sort_key}</span>
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
function AvailabilityBadge({ onDisk, wantedState }: { onDisk: boolean; wantedState?: string }) {
  if (wantedState) {
    const complete = wantedState === "COMPLETE";
    const label =
      wantedState === "PARTIAL" ? "Partial" : wantedState === "UPGRADABLE" ? "Upgradable" : complete ? "Complete" : "Missing";
    return (
      <span
        className={`shrink-0 rounded-full border px-2 py-0.5 text-xs ${
          complete ? "border-current text-accent" : "border-border-subtle text-ink-muted"
        }`}
      >
        {label}
      </span>
    );
  }
  return (
    <span
      className={`shrink-0 rounded-full border px-2 py-0.5 text-xs ${
        onDisk ? "border-current text-accent" : "border-border-subtle text-ink-muted"
      }`}
    >
      {onDisk ? "On disk" : "Not requested"}
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
