import { useEffect, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Search, UserRound } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { remoteImage } from "@/lib/remoteImage";

type Person = components["schemas"]["SeriesPerson"];

/** What a role chip asks for; "" is every role. */
type Group = { label: string; roles: string };

const GROUPS: [Group, ...Group[]] = [
  { label: "Cast", roles: "actor" },
  { label: "Guest stars", roles: "guest_star" },
  { label: "Creators", roles: "creator,showrunner" },
  { label: "Directors", roles: "director" },
  { label: "Writers", roles: "writer" },
  { label: "Producers", roles: "executive_producer,producer" },
  { label: "Composers", roles: "composer" },
  { label: "Everyone", roles: "" },
];

const ROLE_LABEL: Record<string, string> = {
  actor: "Actor",
  guest_star: "Guest star",
  creator: "Creator",
  showrunner: "Showrunner",
  director: "Director",
  writer: "Writer",
  executive_producer: "Executive producer",
  producer: "Producer",
  composer: "Composer",
};

export function roleLabel(role: string): string {
  return ROLE_LABEL[role] ?? role;
}

function PersonTile({ person, categoryId }: { person: Person; categoryId: string }) {
  const subtitle = person.character ?? person.roles.map(roleLabel).join(", ");
  return (
    <Link
      to="/library/$categoryId/people/$personId"
      params={{ categoryId, personId: String(person.id) }}
      className="group flex flex-col items-center gap-1.5 rounded-lg text-center focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      style={{ contentVisibility: "auto", containIntrinsicSize: "auto 200px" }}
    >
      <span className="block aspect-square w-full overflow-hidden rounded-full border border-border-subtle bg-surface group-hover:border-accent">
        {person.image_url ? (
          <img src={remoteImage(person.image_url, "thumb")} alt="" loading="lazy" className="size-full object-cover" />
        ) : (
          <span className="flex size-full items-center justify-center text-ink-muted">
            <UserRound aria-hidden className="size-10" />
          </span>
        )}
      </span>
      <span className="w-full truncate text-sm font-medium">{person.name}</span>
      <span className="w-full truncate text-xs text-ink-muted">{subtitle}</span>
      <span className="text-xs text-ink-muted">
        {person.series_count} series
      </span>
    </Link>
  );
}

/** A TV library's People level: who is in these series, by role, as a wall of faces. */
export function TVPeople() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const [group, setGroup] = useState<Group>(GROUPS[0]);
  const [typed, setTyped] = useState("");
  const [query, setQuery] = useState("");
  useEffect(() => {
    const timer = setTimeout(() => setQuery(typed.trim()), 250);
    return () => clearTimeout(timer);
  }, [typed]);

  const people = useInfiniteQuery({
    queryKey: ["series-people", categoryId, group.roles, query],
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam }) => {
      const { data, error } = await api.GET("/series/people", {
        params: {
          query: {
            category_id: Number(categoryId),
            role: group.roles || undefined,
            q: query || undefined,
            cursor: pageParam,
            limit: 60,
          },
        },
      });
      if (error) throw error;
      return data;
    },
    getNextPageParam: (last) => last.next_cursor,
  });
  const items = people.data?.pages.flatMap((page) => page.items) ?? [];

  return (
    <div className="flex flex-col gap-4 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="mr-2 text-lg font-semibold">People</h1>
        {GROUPS.map((entry) => (
          <button
            key={entry.label}
            type="button"
            aria-pressed={entry === group}
            onClick={() => setGroup(entry)}
            className={`rounded-full border px-3 py-1 text-xs ${
              entry === group ? "border-accent bg-accent/10 text-ink" : "border-border-subtle text-ink-muted hover:text-ink"
            }`}
          >
            {entry.label}
          </button>
        ))}
        <label className="ml-auto flex items-center gap-1.5 rounded-md border border-border-subtle bg-canvas px-2 py-1 text-xs text-ink-muted">
          <Search aria-hidden className="size-3.5" />
          <input
            value={typed}
            onChange={(event) => setTyped(event.target.value)}
            placeholder="Name starts with…"
            aria-label="Filter people by name"
            className="w-44 bg-transparent text-ink outline-none"
          />
        </label>
      </div>
      {people.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {people.isError && <p className="text-sm text-danger">The people could not be loaded.</p>}
      {!people.isLoading && items.length === 0 && (
        <p className="max-w-xl text-sm text-ink-muted">
          Nobody is credited here yet. Cast, crew and guest stars come from TMDB: store your TMDB key under Settings → Metadata, then
          refresh a series.
        </p>
      )}
      <div className="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-4">
        {items.map((person) => (
          <PersonTile key={person.id} person={person} categoryId={categoryId} />
        ))}
      </div>
      {people.hasNextPage && (
        <button
          type="button"
          disabled={people.isFetchingNextPage}
          onClick={() => void people.fetchNextPage()}
          className="mx-auto rounded-md border border-border-subtle px-4 py-1.5 text-sm text-ink-muted enabled:hover:text-ink disabled:opacity-50"
        >
          {people.isFetchingNextPage ? "Loading…" : "Show more"}
        </button>
      )}
    </div>
  );
}
