import { useParams, Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { CoverArt } from "./CoverArt";

type Edition = components["schemas"]["Edition"];
type WorkPart = components["schemas"]["WorkPart"];
type ExternalID = components["schemas"]["ExternalID"];
type Credit = components["schemas"]["Credit"];
type Wanted = components["schemas"]["Wanted"];

// Title, release date, credits, genres, description, editions, parts read
// straight from the work; monitoring (album and per-part) is the one write
// surface (section 19.6). No scoring/Decision yet (stage 4).
export function WorkDetail() {
  const { id: rawID } = useParams({ strict: false }) as { id?: string };
  const id = rawID !== undefined ? Number(rawID) : undefined;
  const queryClient = useQueryClient();

  const detail = useQuery({
    queryKey: ["work", id],
    enabled: id !== undefined,
    queryFn: async () => {
      const { data, error } = await api.GET("/works/{id}", {
        params: { path: { id: id as number } },
      });
      if (error) throw error;
      return data;
    },
  });

  const invalidate = () => void queryClient.invalidateQueries({ queryKey: ["work", id] });

  const setMonitored = useMutation({
    mutationFn: async (monitored: boolean) => {
      if (id === undefined) return;
      const { error } = await api.POST("/works/{id}/monitor", {
        params: { path: { id } },
        body: { monitored },
      });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  const setPartMonitored = useMutation({
    mutationFn: async ({ partId, monitored }: { partId: number; monitored: boolean }) => {
      if (id === undefined) return;
      const { error } = await api.POST("/works/{id}/parts/{partId}/monitor", {
        params: { path: { id, partId } },
        body: { monitored },
      });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  if (id === undefined) return <Message>No work id in the URL.</Message>;
  if (detail.isLoading) return <Message>Loading…</Message>;
  if (detail.error !== null || !detail.data) {
    return <Message>This work could not be read.</Message>;
  }

  const work = detail.data;

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-3xl flex-col gap-6 p-6">
        <header className="flex items-start gap-4">
          <CoverArt
            workId={work.id}
            title={work.title}
            size="full"
            className="h-40 w-40 shrink-0 rounded-lg object-cover"
          />
          <div className="flex flex-1 items-start justify-between gap-4 pt-1">
            <div className="flex flex-col gap-1.5">
              <h1 className="text-lg font-semibold">{work.title}</h1>
              <p className="text-sm text-ink-muted">
                {work.year ? `Released ${work.year}` : "Release date unknown"}
              </p>
              <WantedBadge wanted={work.wanted} onDisk={work.on_disk} />
            </div>
            <button
              type="button"
              disabled={setMonitored.isPending}
              onClick={() => setMonitored.mutate(!(work.wanted?.monitored ?? false))}
              className="shrink-0 rounded-md border border-border-subtle px-3 py-1.5 text-sm
                         enabled:hover:bg-surface disabled:cursor-default disabled:text-ink-muted"
            >
              {work.wanted?.monitored
                ? "Monitored"
                : setMonitored.isPending
                  ? "…"
                  : "Monitor"}
            </button>
          </div>
        </header>

        {work.description && (
          <Section title="Description">
            <p className="text-sm leading-relaxed">{work.description}</p>
          </Section>
        )}

        {work.credits.length > 0 && (
          <Section title="Credits">
            <CreditList credits={work.credits} />
          </Section>
        )}

        {work.genres.length > 0 && (
          <Section title="Genres">
            <ul className="flex flex-wrap gap-1.5">
              {work.genres.map((genre) => (
                <li
                  key={genre}
                  className="rounded-full border border-border-subtle px-2 py-0.5 text-xs text-ink-muted"
                >
                  {genre}
                </li>
              ))}
            </ul>
          </Section>
        )}

        {work.editions.length > 0 && (
          <Section title={`Editions (${work.editions.length})`}>
            <ul className="flex flex-col gap-2">
              {work.editions.map((edition) => (
                <EditionCard key={edition.id} edition={edition} />
              ))}
            </ul>
          </Section>
        )}

        {work.parts.length > 0 && (
          <Section title={`Parts (${work.parts.length})`}>
            <ul
              className="divide-y divide-border-subtle rounded-lg border
                         border-border-subtle bg-surface"
            >
              {work.parts.map((part) => (
                <PartRow
                  key={part.id}
                  part={part}
                  pending={setPartMonitored.isPending}
                  onToggleMonitored={(monitored) =>
                    setPartMonitored.mutate({ partId: part.id, monitored })
                  }
                />
              ))}
            </ul>
          </Section>
        )}

        {work.external_ids.length > 0 && (
          <Section title="External IDs">
            <ExternalIDList ids={work.external_ids} />
          </Section>
        )}
      </div>
    </div>
  );
}

// WantedBadge renders the real per-track fact (section 19.6's "9 of 12
// tracks") once something has ever wanted this album, falling back to
// on_disk's coarser badge otherwise.
function WantedBadge({
  wanted,
  onDisk,
}: {
  wanted: Wanted | null | undefined;
  onDisk: boolean;
}) {
  if (!wanted) return <OnDiskBadge onDisk={onDisk} />;

  const label =
    wanted.required_count != null
      ? `${wanted.present_count} of ${wanted.required_count} tracks`
      : wanted.state === "COMPLETE"
        ? "Complete"
        : wanted.present_count > 0
          ? `${wanted.present_count} tracks`
          : "Missing";

  return (
    <span
      className={`w-fit rounded-full border px-2 py-0.5 text-xs ${
        wanted.state === "COMPLETE"
          ? "border-current text-accent"
          : "border-border-subtle text-ink-muted"
      }`}
    >
      {label}
    </span>
  );
}

function OnDiskBadge({ onDisk }: { onDisk: boolean }) {
  return (
    <span
      className={`w-fit rounded-full border px-2 py-0.5 text-xs ${
        onDisk
          ? "border-current text-accent"
          : "border-border-subtle text-ink-muted"
      }`}
    >
      {onDisk ? "On disk" : "Missing"}
    </span>
  );
}

// Grouped by role (artist, producer, songwriter, ...) so a long credit list
// reads as who-did-what rather than a flat name dump.
function CreditList({ credits }: { credits: readonly Credit[] }) {
  const byRole = new Map<string, Credit[]>();
  for (const credit of credits) {
    const group = byRole.get(credit.role) ?? [];
    group.push(credit);
    byRole.set(credit.role, group);
  }

  return (
    <dl className="flex flex-col gap-2">
      {[...byRole.entries()].map(([role, people]) => (
        <div key={role} className="flex items-baseline gap-2 text-sm">
          <dt className="w-24 shrink-0 capitalize text-ink-muted">{role}</dt>
          <dd className="flex flex-wrap gap-x-2 gap-y-1">
            {people.map((credit) => (
              <Link
                key={credit.person_id}
                to="/people/$id"
                params={{ id: String(credit.person_id) }}
                className="hover:underline"
              >
                {credit.person_name}
              </Link>
            ))}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function EditionCard({ edition }: { edition: Edition }) {
  const fields = [
    edition.format,
    edition.catalogue_number,
    edition.barcode,
    edition.year ? String(edition.year) : undefined,
  ].filter((field): field is string => Boolean(field));

  return (
    <li className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface p-3">
      <div className="flex items-center gap-2">
        <span className="font-medium">{edition.label}</span>
        <span className="text-xs text-ink-muted">{edition.kind}</span>
        {edition.is_default && (
          <span className="rounded-full border border-current px-1.5 py-px text-xs text-accent">
            default
          </span>
        )}
      </div>
      {fields.length > 0 && <p className="text-xs text-ink-muted">{fields.join(" · ")}</p>}
      {edition.external_ids.length > 0 && <ExternalIDList ids={edition.external_ids} />}
    </li>
  );
}

function PartRow({
  part,
  pending,
  onToggleMonitored,
}: {
  part: WorkPart;
  pending: boolean;
  onToggleMonitored: (monitored: boolean) => void;
}) {
  return (
    <li className="flex items-center gap-3 px-3 py-2 text-sm">
      <button
        type="button"
        role="switch"
        aria-checked={part.monitored}
        aria-label={part.monitored ? "Unmonitor this track" : "Monitor this track"}
        disabled={pending}
        onClick={() => onToggleMonitored(!part.monitored)}
        className={`h-3.5 w-3.5 shrink-0 rounded-sm border disabled:cursor-default ${
          part.monitored
            ? "border-accent bg-accent"
            : "border-border-subtle bg-transparent"
        }`}
      />
      <span className="w-10 shrink-0 font-mono text-xs text-ink-muted">{part.sort_key}</span>
      <span className="truncate">{part.title ?? part.media_ref}</span>
      {part.runtime_ms != null && (
        <span className="ml-auto shrink-0 font-mono text-xs text-ink-muted">
          {formatDuration(part.runtime_ms)}
        </span>
      )}
    </li>
  );
}

function ExternalIDList({ ids }: { ids: readonly ExternalID[] }) {
  return (
    <ul className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-ink-muted">
      {ids.map((id) => (
        <li key={`${id.provider}:${id.external_id}`} className="font-mono">
          {id.provider}: {id.external_id}
        </li>
      ))}
    </ul>
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
