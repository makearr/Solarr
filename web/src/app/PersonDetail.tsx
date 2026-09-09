import { useParams, Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type PersonWork = components["schemas"]["PersonWork"];

// Read-only aside from monitoring: a person's own fields, biography, aliases
// and discography (ADR-32). No coverage (held/missing/upgradable) yet -- that
// needs desired_item for people, which stage 4 has not built.
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

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-3xl flex-col gap-6 p-6">
        <header className="flex items-start justify-between gap-4">
          <div className="flex flex-col gap-1">
            <h1 className="text-lg font-semibold">{person.name}</h1>
            {facts.length > 0 && <p className="text-sm text-ink-muted">{facts.join(" · ")}</p>}
            {person.aliases.length > 0 && (
              <p className="text-xs text-ink-muted">
                Also known as {person.aliases.join(", ")}
              </p>
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

        {person.discography.length > 0 && (
          <Section title={`Discography (${person.discography.length})`}>
            <ul
              className="divide-y divide-border-subtle rounded-lg border
                         border-border-subtle bg-surface"
            >
              {person.discography.map((work) => (
                <DiscographyRow key={work.id} work={work} />
              ))}
            </ul>
          </Section>
        )}
      </div>
    </div>
  );
}

function DiscographyRow({ work }: { work: PersonWork }) {
  return (
    <li className="flex items-center gap-3 px-3 py-2 text-sm">
      <Link to="/works/$id" params={{ id: String(work.id) }} className="truncate hover:underline">
        {work.title}
      </Link>
      {work.year != null && (
        <span className="ml-auto shrink-0 text-xs text-ink-muted">{work.year}</span>
      )}
    </li>
  );
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
