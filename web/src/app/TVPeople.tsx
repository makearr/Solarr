import { useEffect, useMemo, useRef, useState } from "react";
import { useParams } from "@tanstack/react-router";
import { Search } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { remoteImage } from "@/lib/remoteImage";
import { usePreference } from "@/lib/usePreference";
import { PosterGrid } from "@/list/PosterGrid";
import { useGridMetrics } from "@/list/useGridMetrics";
import { useLibraryList } from "@/list/useLibraryList";
import type { ViewState } from "@/list/useRouteView";
import { useRouteView } from "@/list/useRouteView";

import { DEFAULT_POSTER_OPTIONS, POSTER_GAP, POSTER_PADDING, POSTER_WIDTHS, PosterOptionsMenu, posterRowHeight, type PosterOptions } from "./IndexViews";
import { Poster } from "./Poster";

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

const FILTER_KEYS: readonly string[] = ["role", "monitored", "q"];

/** A TV library's People level: who is in these series, by role, as the same wall of faces the music Artists level is. */
export function TVPeople() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const baseView = useRouteView(FILTER_KEYS);
  const view = useMemo<ViewState>(() => baseView, [baseView]);
  const role = view.query.filters.role ?? GROUPS[0].roles;
  const keyOf = (roles: string) => (roles === "" ? "all" : roles);
  const [storedOptions, setOptions] = usePreference<PosterOptions>("posters.options", DEFAULT_POSTER_OPTIONS);
  const options = { ...DEFAULT_POSTER_OPTIONS, ...storedOptions };
  const frameRef = useRef<HTMLDivElement>(null);
  const metrics = useGridMetrics(frameRef, POSTER_WIDTHS[options.size], POSTER_GAP, POSTER_PADDING);

  const [typed, setTyped] = useState(view.query.filters.q ?? "");
  useEffect(() => {
    const timer = setTimeout(() => {
      if (typed.trim() !== (view.query.filters.q ?? "")) view.setQuery({ ...view.query, filters: { ...view.query.filters, q: typed.trim() } });
    }, 250);
    return () => clearTimeout(timer);
  }, [typed, view]);

  const list = useLibraryList<Person>(
    {
      name: `series-people-${categoryId}`,
      idOf: (person) => person.id,
      rowHeight: posterRowHeight({ ...options, showAvailability: false }, metrics.cellWidth),
      itemsPerRow: metrics.columns,
      fetchPage: async ({ cursor, query, signal }) => {
        const filters = query.filters;
        const { data, error } = await api.GET("/series/people", {
          params: {
            query: {
              category_id: Number(categoryId),
              role: (filters.role ?? GROUPS[0].roles) === "all" ? undefined : (filters.role ?? GROUPS[0].roles) || undefined,
              q: filters.q || undefined,
              monitored: filters.monitored ? filters.monitored === "true" : undefined,
              cursor,
              limit: 100,
            },
          },
          signal,
        });
        if (error) throw error;
        return data;
      },
    },
    view,
  );

  const setFilter = (key: string, value: string) => view.setQuery({ ...view.query, filters: { ...view.query.filters, [key]: value } });
  const monitored = view.query.filters.monitored ?? "";

  return (
    <div ref={frameRef} className="flex h-full flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border-subtle px-4 py-2">
        <h2 className="mr-2 text-sm font-medium">People</h2>
        {GROUPS.map((entry) => (
          <button
            key={entry.label}
            type="button"
            aria-pressed={keyOf(entry.roles) === role}
            onClick={() => setFilter("role", entry.roles === GROUPS[0].roles ? "" : keyOf(entry.roles))}
            className={`rounded-full border px-3 py-1 text-xs ${
              keyOf(entry.roles) === role ? "border-accent bg-accent/10 text-ink" : "border-border-subtle text-ink-muted hover:text-ink"
            }`}
          >
            {entry.label}
          </button>
        ))}
        <div className="ml-auto flex items-center gap-2 text-xs">
          <label className="flex items-center gap-1 text-ink-muted">
            Show
            <select
              value={monitored}
              onChange={(event) => setFilter("monitored", event.target.value)}
              className="rounded-md border border-border-subtle bg-surface px-1.5 py-1 text-ink"
            >
              <option value="">All people</option>
              <option value="true">Followed</option>
              <option value="false">Not followed</option>
            </select>
          </label>
          <label className="flex items-center gap-1.5 rounded-md border border-border-subtle bg-canvas px-2 py-1 text-ink-muted">
            <Search aria-hidden className="size-3.5" />
            <input
              value={typed}
              onChange={(event) => setTyped(event.target.value)}
              placeholder="Name starts with…"
              aria-label="Filter people by name"
              className="w-40 bg-transparent text-ink outline-none"
            />
          </label>
          <PosterOptionsMenu options={options} onChange={setOptions} />
        </div>
      </div>
      <div className="min-h-0 flex-1">
        {list.isLoading && <p className="p-4 text-sm text-ink-muted">Loading…</p>}
        {list.error !== null && <p className="p-4 text-sm text-danger">The people could not be loaded.</p>}
        {!list.isLoading && list.error === null && list.items.length === 0 && (
          <p className="max-w-xl p-4 text-sm text-ink-muted">
            {monitored === "true"
              ? "Nobody is followed yet. Open a person and choose Follow to have their new series added for you."
              : monitored === "false"
                ? "Everyone listed here is followed."
                : "Nobody is credited here yet. Cast, crew and guest stars come from TMDB: store your TMDB key under Settings → Metadata, then refresh a series."}
          </p>
        )}
        {list.items.length > 0 && (
          <PosterGrid
            list={list}
            idOf={(person) => person.id}
            renderPoster={(person) => (
              <Poster
                subject="people"
                imageId={null}
                imageSrc={person.image_url ? remoteImage(person.image_url, "small") : undefined}
                title={person.name}
                subtitle={`${person.character ?? person.roles.map(roleLabel).join(", ")} · ${person.series_count} ${person.series_count === 1 ? "series" : "series"}`}
                link={{ to: "/library/$categoryId/people/$personId", params: { categoryId, personId: String(person.id) } }}
                round
                showTitle={options.showTitle}
              />
            )}
          />
        )}
      </div>
    </div>
  );
}
