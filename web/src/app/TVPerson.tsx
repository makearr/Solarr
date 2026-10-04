import { useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Disc3, Film, Tv, UserRound } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

import { CoverArt } from "./CoverArt";
import { AddPanel } from "./AddSeries";
import { FollowPanel } from "./FollowPanel";
import { roleLabel } from "./TVPeople";
import { remoteImage } from "@/lib/remoteImage";

type Credit = components["schemas"]["SeriesPersonCredit"];
type Elsewhere = components["schemas"]["SeriesPersonElsewhere"];

function Poster({ url, kind }: { url?: string; kind: "tv" | "movie" }) {
  const Icon = kind === "tv" ? Tv : Film;
  return (
    <span className="block aspect-[2/3] overflow-hidden rounded-lg border border-border-subtle bg-surface">
      {url ? (
        <img src={remoteImage(url, "small")} alt="" loading="lazy" className="size-full object-cover" />
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

/** A title TMDB knows the person for: in the library it opens the series, a series not held is added to Solarr from here. */
function ElsewhereTile({ title, categoryId }: { title: Elsewhere; categoryId: string }) {
  const [adding, setAdding] = useState(false);
  const caption = (
    <>
      <Poster url={title.poster_url} kind={title.media_type} />
      <span className="truncate text-sm font-medium">
        {title.title}
        {title.year ? <span className="font-normal text-ink-muted"> ({title.year})</span> : null}
      </span>
      <span className="truncate text-xs text-ink-muted">{title.role}</span>
    </>
  );
  if (title.library_series_id) {
    return (
      <Link
        to="/library/$categoryId/series/$seriesId"
        params={{ categoryId, seriesId: String(title.library_series_id) }}
        className="flex flex-col gap-1 focus-visible:outline-2 focus-visible:outline-accent"
      >
        {caption}
        <span className="text-xs text-ok">In your library</span>
      </Link>
    );
  }
  if (title.media_type === "movie") {
    return (
      <a
        href={`https://www.themoviedb.org/movie/${title.tmdb_id}`}
        target="_blank"
        rel="noreferrer"
        className="flex flex-col gap-1 focus-visible:outline-2 focus-visible:outline-accent"
      >
        {caption}
        <span className="text-xs text-ink-muted">Movies arrive with the Movies library</span>
      </a>
    );
  }
  return (
    <div className="flex flex-col gap-1">
      {caption}
      <button
        type="button"
        aria-expanded={adding}
        onClick={() => setAdding(!adding)}
        className="w-fit rounded-md border border-border-subtle px-2 py-0.5 text-xs text-accent hover:border-accent"
      >
        Add to Solarr
      </button>
      {adding && (
        <AddPanel
          candidate={{ provider: "tmdb", external_id: String(title.tmdb_id), title: title.title }}
          categoryId={categoryId}
          onDone={() => setAdding(false)}
        />
      )}
    </div>
  );
}

const MEDIA_TYPE_LABEL: Record<string, string> = { tv: "Series", movie: "Movies", music: "Music" };

/** One person of a TV library: who they are, what they did in these series, and what else they are known for. */
export function TVPerson() {
  const { categoryId, personId } = useParams({ strict: false }) as { categoryId: string; personId: string };
  const [role, setRole] = useState("");
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
  const roleKeys = [...new Set(data.credits.map((credit) => credit.role))];
  const shown = role ? data.credits.filter((credit) => credit.role === role) : data.credits;
  const seriesCount = new Set(shown.map((credit) => credit.series_id)).size;
  const links: [string, string, string | undefined][] = [
    ["TMDB", "tmdb", data.ids.tmdb ? `https://www.themoviedb.org/person/${data.ids.tmdb}` : undefined],
    ["IMDb", "imdb", data.ids.imdb ? `https://www.imdb.com/name/${data.ids.imdb}/` : undefined],
  ];

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-4">
      <Link to="/library/$categoryId/$level" params={{ categoryId, level: "people" }} className="text-xs text-ink-muted hover:text-ink">
        ← Cast
      </Link>
      <div className="flex gap-4">
        <span className="block size-40 shrink-0 overflow-hidden rounded-full border border-border-subtle bg-surface">
          {data.image_url ? (
            <img src={remoteImage(data.image_url, "small")} alt="" className="size-full object-cover" />
          ) : data.other_works && data.other_works.length > 0 ? (
            <CoverArt subject="people" workId={data.id} title={data.name} size="full" className="size-full object-cover" />
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
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="mr-2 text-sm font-semibold">
            In this library <span className="font-normal text-ink-muted">· {seriesCount} series</span>
          </h2>
          {roleKeys.length > 1 &&
            ["", ...roleKeys].map((key) => (
              <button
                key={key || "all"}
                type="button"
                aria-pressed={key === role}
                onClick={() => setRole(key)}
                className={`rounded-full border px-3 py-1 text-xs ${
                  key === role ? "border-accent bg-accent/10 text-ink" : "border-border-subtle text-ink-muted hover:text-ink"
                }`}
              >
                {key ? roleLabel(key) : "All"}
              </button>
            ))}
        </div>
        <div className="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-4">
          {shown.map((credit) => (
            <CreditTile key={`${credit.series_id}-${credit.role}-${credit.character ?? ""}`} credit={credit} categoryId={categoryId} />
          ))}
        </div>
      </section>

      {data.other_works && data.other_works.length > 0 && (
        <section className="flex flex-col gap-3">
          <div className="flex items-baseline justify-between">
            <h2 className="text-sm font-semibold">Also in your other libraries</h2>
            <Link to="/people/$id" params={{ id: String(data.id) }} className="text-xs text-accent hover:underline">
              Open full profile
            </Link>
          </div>
          {[...new Set(data.other_works.map((work) => work.media_type))].map((type) => (
            <div key={type} className="flex flex-col gap-1.5">
              <h3 className="text-xs font-medium text-ink-muted">{MEDIA_TYPE_LABEL[type] ?? type}</h3>
              <ul className="grid grid-cols-[repeat(auto-fill,minmax(14rem,1fr))] gap-2">
                {(data.other_works ?? [])
                  .filter((work) => work.media_type === type)
                  .map((work) => (
                    <li key={`${work.work_id}-${work.role}`}>
                      <Link
                        to="/works/$id"
                        params={{ id: String(work.work_id) }}
                        className="flex items-center gap-2 rounded-lg border border-border-subtle bg-surface p-2 text-sm hover:border-accent"
                      >
                        <Disc3 aria-hidden className="size-4 shrink-0 text-ink-muted" />
                        <span className="min-w-0 flex-1 truncate">
                          {work.title}
                          {work.year ? <span className="text-ink-muted"> ({work.year})</span> : null}
                        </span>
                        <span className="text-xs text-ink-muted">{roleLabel(work.role)}</span>
                      </Link>
                    </li>
                  ))}
              </ul>
            </div>
          ))}
        </section>
      )}

      {data.elsewhere && data.elsewhere.length > 0 && (
        <section className="flex flex-col gap-4">
          <h2 className="text-sm font-semibold">Also known for</h2>
          {(["tv", "movie"] as const).map((type) => {
            const titles = (data.elsewhere ?? []).filter((title) => title.media_type === type);
            if (titles.length === 0) return null;
            return (
              <div key={type} className="flex flex-col gap-2">
                <h3 className="text-xs font-medium text-ink-muted">{MEDIA_TYPE_LABEL[type]}</h3>
                <div className="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-4">
                  {titles.map((title) => (
                    <ElsewhereTile key={`${title.media_type}-${title.tmdb_id}`} title={title} categoryId={categoryId} />
                  ))}
                </div>
              </div>
            );
          })}
        </section>
      )}
    </div>
  );
}
