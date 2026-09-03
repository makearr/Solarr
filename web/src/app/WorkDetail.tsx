import { useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type Edition = components["schemas"]["Edition"];
type WorkPart = components["schemas"]["WorkPart"];
type ExternalID = components["schemas"]["ExternalID"];

// Read-only structural view (title, editions, parts, external ids per level).
// No scoring, decision or candidate history: that renders from a persisted
// Decision object (section 21.3) stage 4's acquisition planner has not built.
export function WorkDetail() {
  const { id: rawID } = useParams({ strict: false }) as { id?: string };
  const id = rawID !== undefined ? Number(rawID) : undefined;

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

  if (id === undefined) return <Message>No work id in the URL.</Message>;
  if (detail.isLoading) return <Message>Loading…</Message>;
  if (detail.error !== null || !detail.data) {
    return <Message>This work could not be read.</Message>;
  }

  const work = detail.data;

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-3xl flex-col gap-6 p-6">
        <header className="flex flex-col gap-1">
          <h1 className="text-lg font-semibold">{work.title}</h1>
          <p className="text-sm text-ink-muted">
            {work.media_type}
            {work.year ? ` · ${work.year}` : ""}
          </p>
        </header>

        {work.external_ids.length > 0 && (
          <Section title="External IDs">
            <ExternalIDList ids={work.external_ids} />
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
                <PartRow key={part.id} part={part} />
              ))}
            </ul>
          </Section>
        )}
      </div>
    </div>
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

function PartRow({ part }: { part: WorkPart }) {
  return (
    <li className="flex items-center gap-3 px-3 py-2 text-sm">
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
