import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { Search, User } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { remoteImage } from "@/lib/remoteImage";
import { usePreference } from "@/lib/usePreference";
import type { FilterField } from "@/list/customFilter";
import { ListTable, type Column } from "@/list/ListTable";
import { PosterGrid } from "@/list/PosterGrid";
import { useGridMetrics } from "@/list/useGridMetrics";
import { useLibraryList } from "@/list/useLibraryList";
import type { ViewState } from "@/list/useRouteView";
import { useRouteView } from "@/list/useRouteView";

import {
  DEFAULT_POSTER_OPTIONS, IndexViewToggle, OVERVIEW_ROW_HEIGHT, POSTER_GAP, POSTER_PADDING, POSTER_WIDTHS, PosterOptionsMenu, posterRowHeight,
  type IndexView, type PosterOptions,
} from "./IndexViews";
import { CustomFilterMenu } from "./CustomFilterMenu";
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

const FILTER_KEYS: readonly string[] = ["role", "monitored", "q", "where"];

const PERSON_FILTER_FIELDS: readonly FilterField[] = [
  { key: "name", label: "Name", kind: "text" },
  { key: "role", label: "Role", kind: "enum", choices: Object.entries(ROLE_LABEL) },
  { key: "series", label: "Series in this library", kind: "number" },
  { key: "monitored", label: "Followed", kind: "bool" },
  { key: "photo", label: "Has a photo", kind: "bool" },
];

type SortKey = "name" | "series";
const SORT_LABEL: Record<SortKey, string> = { name: "Name", series: "Series in this library" };

/** A TV library's People level: who is in these series, by role, as the same wall of faces the music Artists level is. */
export function TVPeople() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const baseView = useRouteView(FILTER_KEYS);
  const view = useMemo<ViewState>(() => baseView, [baseView]);
  const role = view.query.filters.role ?? GROUPS[0].roles;
  const keyOf = (roles: string) => (roles === "" ? "all" : roles);
  const [storedMode, setMode] = usePreference<IndexView>(`people.view.${categoryId}`, "poster");
  const mode: IndexView = storedMode === "list" || storedMode === "overview" ? storedMode : "poster";
  const sort: SortKey = view.query.sort === "series" ? "series" : "name";
  const direction = view.query.direction;
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
      rowHeight: mode === "poster" ? posterRowHeight({ ...options, showAvailability: false }, metrics.cellWidth) : mode === "overview" ? OVERVIEW_ROW_HEIGHT / 1.6 : 44,
      itemsPerRow: mode === "poster" ? metrics.columns : 1,
      fetchPage: async ({ cursor, query, signal }) => {
        const filters = query.filters;
        const { data, error } = await api.GET("/series/people", {
          params: {
            query: {
              category_id: Number(categoryId),
              role: (filters.role ?? GROUPS[0].roles) === "all" ? undefined : (filters.role ?? GROUPS[0].roles) || undefined,
              q: filters.q || undefined,
              monitored: filters.monitored ? filters.monitored === "true" : undefined,
              sort: query.sort === "series" ? "series" : "name",
              dir: query.direction,
              where: filters.where || undefined,
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
  const columns: readonly Column<Person>[] = [
    {
      key: "name", header: "Name", sortable: true, width: "minmax(12rem,2fr)",
      render: (person) => (
        <Link to="/library/$categoryId/people/$personId" params={{ categoryId, personId: String(person.id) }} className="flex items-center gap-2 hover:underline" onClick={(event) => event.stopPropagation()}>
          <span className="block size-7 shrink-0 overflow-hidden rounded-full border border-border-subtle bg-surface">
            {person.image_url ? <img src={remoteImage(person.image_url, "thumb")} alt="" loading="lazy" className="size-full object-cover" /> : <User aria-hidden className="m-1 size-5 text-ink-muted" />}
          </span>
          {person.name}
        </Link>
      ),
    },
    { key: "roles", header: "Roles", width: "minmax(8rem,1.5fr)", render: (person) => person.roles.map(roleLabel).join(", ") },
    { key: "character", header: "As", width: "minmax(8rem,1.5fr)", render: (person) => person.character ?? "" },
    { key: "series", header: "Series", sortable: true, width: "6rem", render: (person) => person.series_count },
    { key: "followed", header: "Followed", width: "6rem", render: (person) => (person.monitored ? "Yes" : "") },
  ];

  return (
    <div ref={frameRef} className="flex h-full flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border-subtle px-4 py-2">
        <h2 className="mr-2 text-sm font-medium">Cast</h2>
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
              <option value="">Everyone</option>
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
              aria-label="Filter cast by name"
              className="w-40 bg-transparent text-ink outline-none"
            />
          </label>
          <CustomFilterMenu fields={PERSON_FILTER_FIELDS} storageKey={`cast.${categoryId}`} where={view.query.filters.where ?? ""} onChange={(where) => setFilter("where", where)} />
          <label className="flex items-center gap-1 text-ink-muted">
            Sort
            <select
              value={sort}
              onChange={(event) => view.setQuery({ ...view.query, sort: event.target.value, direction: event.target.value === "series" ? "desc" : "asc" })}
              className="rounded-md border border-border-subtle bg-surface px-1.5 py-1 text-ink"
            >
              {(Object.keys(SORT_LABEL) as SortKey[]).map((key) => (
                <option key={key} value={key}>
                  {SORT_LABEL[key]}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            aria-label={direction === "asc" ? "Ascending: turn round" : "Descending: turn round"}
            title="Turn the order round"
            onClick={() => view.setQuery({ ...view.query, sort, direction: direction === "asc" ? "desc" : "asc" })}
            className="rounded-md border border-border-subtle px-1.5 py-1 text-ink-muted hover:text-ink"
          >
            {direction === "asc" ? "▲" : "▼"}
          </button>
          <IndexViewToggle view={mode} onChange={setMode} />
          {mode === "poster" && <PosterOptionsMenu options={options} onChange={setOptions} />}
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
        {list.items.length > 0 && mode === "list" && <ListTable list={list} columns={columns} idOf={(person) => person.id} />}
        {list.items.length > 0 && mode === "overview" && (
          <div ref={list.scrollRef} className="h-full overflow-auto">
            <div style={{ height: list.totalSize, position: "relative" }}>
              {list.virtualItems.map((virtual) => {
                const person = list.items[virtual.index];
                if (!person) return null;
                return (
                  <div key={person.id} className="absolute inset-x-0 px-4" style={{ height: virtual.size, transform: `translateY(${virtual.start}px)` }}>
                    <Link to="/library/$categoryId/people/$personId" params={{ categoryId, personId: String(person.id) }} className="flex h-full items-center gap-4 border-b border-border-subtle hover:bg-surface">
                      <span className="block size-16 shrink-0 overflow-hidden rounded-full border border-border-subtle bg-surface">
                        {person.image_url ? <img src={remoteImage(person.image_url, "small")} alt="" loading="lazy" className="size-full object-cover" /> : <User aria-hidden className="m-4 size-8 text-ink-muted" />}
                      </span>
                      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                        <span className="truncate text-base font-medium">{person.name}</span>
                        <span className="truncate text-sm text-ink-muted">{person.roles.map(roleLabel).join(", ")}{person.character ? ` · as ${person.character}` : ""}</span>
                        <span className="text-xs text-ink-muted">{person.series_count} series{person.monitored ? " · followed" : ""}</span>
                      </span>
                    </Link>
                  </div>
                );
              })}
            </div>
          </div>
        )}
        {list.items.length > 0 && mode === "poster" && (
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
