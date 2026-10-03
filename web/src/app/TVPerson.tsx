import { Link, useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Film, Tv, UserRound } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

import { FollowPanel } from "./FollowPanel";
import { roleLabel } from "./TVPeople";

type Credit = components["schemas"]["SeriesPersonCredit"];
type Elsewhere = components["schemas"]["SeriesPersonElsewhere"];

function Poster({ url, kind }: { url?: string; kind: "tv" | "movie" }) {
  const Icon = kind === "tv" ? Tv : Film;
  return (
    <span className="block aspect-[2/3] overflow-hidden rounded-lg border border-border-subtle bg-surface">
      {url ? (
        <img src={url} alt="" loading="lazy" className="size-full object-cover" referrerPolicy="no-referrer" />
      ) : (
        <span className="flex size-full items-center justify-center text-ink-muted">
          <Icon aria-hidden className="size-8" />
        </span>
      )}
    </span>
  );
}

function CreditTile({ credit, categoryId }: { credit: Credit; categoryId: string }) {
  const what = [roleLabel(credit.role), credit.character].filter(Boolean).join(" · ");
  const episodes = credit.episodes ?? [];
  return (
    <Link
      to="/library/$categoryId/series/$seriesId"
      params={{ categoryId, seriesId: String(credit.series_id) }}
      className="group flex flex-col gap-1 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
    >
      <Poster url={credit.poster_url} kind="tv" />
      <span className="truncate text-sm font-medium">
        {credit.series_title}
        {credit.year ? <span className="font-normal text-ink-muted"> ({credit.year})</span> : null}
      </span>
      <span className="truncate text-xs text-ink-muted">{what}</span>
      {episodes.length > 0 && (
        <span className="truncate text-xs text-ink-muted" title={episodes.join(", ")}>
          {episodes.length <= 3 ? episodes.join(", ") : `${episodes.length} episodes`}
        </span>
      )}
    </Link>
  );
}

function ElsewhereTile({ title }: { title: Elsewhere }) {
  const href = `https://www.themoviedb.org/${title.media_type}/${title.tmdb_id}`;
  return (
    <a href={href} target="_blank" rel="noreferrer" className="flex flex-col gap-1 focus-visible:outline-2 focus-visible:outline-accent">
      <Poster url={title.poster_url} kind={title.media_type} />
      <span className="truncate text-sm font-medium">
        {title.title}
        {title.year ? <span className="font-normal text-ink-muted"> ({title.year})</span> : null}
      </span>
      <span className="truncate text-xs text-ink-muted">{title.role}</span>
    </a>
  );
}

/** One person of a TV library: who they are, what they did in these series, and what else they are known for. */
export function TVPerson() {
  const { categoryId, personId } = useParams({ strict: false }) as { categoryId: string; personId: string };
  const person = useQuery({
    queryKey: ["series-person", categoryId, personId],
    queryFn: async () => {
      const { data, error } = await api.GET("/series/people/{id}", {
        params: { path: { id: Number(personId) }, query: { category_id: Number(categoryId) } },
      });
      if (error) throw error;
      return data;
    },
  });

  if (person.isLoading) return <p className="p-4 text-sm text-ink-muted">Loading…</p>;
  if (person.isError || !person.data) return <p className="p-4 text-sm text-danger">This person could not be found.</p>;

  const data = person.data;
  const dates = [data.born && `Born ${data.born}`, data.died && `Died ${data.died}`, data.birthplace].filter(Boolean);
  const roles = [...new Set(data.credits.map((credit) => roleLabel(credit.role)))];
  const links: [string, string, string | undefined][] = [
    ["TMDB", "tmdb", data.ids.tmdb ? `https://www.themoviedb.org/person/${data.ids.tmdb}` : undefined],
    ["IMDb", "imdb", data.ids.imdb ? `https://www.imdb.com/name/${data.ids.imdb}/` : undefined],
  ];

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-4">
      <Link to="/library/$categoryId/$level" params={{ categoryId, level: "people" }} className="text-xs text-ink-muted hover:text-ink">
        ← People
      </Link>
      <div className="flex gap-4">
        <span className="block size-40 shrink-0 overflow-hidden rounded-full border border-border-subtle bg-surface">
          {data.image_url ? (
            <img src={data.image_url} alt="" className="size-full object-cover" referrerPolicy="no-referrer" />
          ) : (
            <span className="flex size-full items-center justify-center text-ink-muted">
              <UserRound aria-hidden className="size-14" />
            </span>
          )}
        </span>
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          <h1 className="text-xl font-semibold">{data.name}</h1>
          <p className="text-sm text-ink-muted">{[...roles, ...dates].join(" · ")}</p>
          {data.biography ? (
            <p className="max-w-3xl whitespace-pre-line text-sm text-ink-muted">{data.biography}</p>
          ) : (
            <p className="text-sm text-ink-muted">No biography yet. It comes from TMDB once a key is stored under Settings → Metadata.</p>
          )}
          <FollowPanel
            kind="person"
            subjectId={data.id}
            categoryId={categoryId}
            follow={data.follow}
            followable={data.followable}
            suggestedRoles={[...new Set(data.credits.map((credit) => credit.role).filter((role) => role !== "guest_star"))]}
          />
          <div className="mt-auto flex gap-3 text-xs">
            {links.map(([label, , href]) =>
              href ? (
                <a key={label} href={href} target="_blank" rel="noreferrer" className="text-accent hover:underline">
                  {label}
                </a>
              ) : null,
            )}
          </div>
        </div>
      </div>

      <section className="flex flex-col gap-3">
        <h2 className="text-sm font-semibold">In this library</h2>
        <div className="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-4">
          {data.credits.map((credit) => (
            <CreditTile key={`${credit.series_id}-${credit.role}-${credit.character ?? ""}`} credit={credit} categoryId={categoryId} />
          ))}
        </div>
      </section>

      {data.elsewhere && data.elsewhere.length > 0 && (
        <section className="flex flex-col gap-3">
          <h2 className="text-sm font-semibold">Also known for</h2>
          <div className="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-4">
            {data.elsewhere.map((title) => (
              <ElsewhereTile key={`${title.media_type}-${title.tmdb_id}`} title={title} />
            ))}
          </div>
        </section>
      )}
    </div>
  );
}
