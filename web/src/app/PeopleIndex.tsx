import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { Plus, Search, User } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import type { AlbumStatus } from "@/lib/albumStatus";
import { remoteImage } from "@/lib/remoteImage";
import { useLibrary } from "@/lib/useLibrary";
import { useMediaTypes, type MediaTypeInfo } from "@/lib/useMediaTypes";
import { usePreference } from "@/lib/usePreference";
import type { FilterField } from "@/list/customFilter";
import { ListTable, type Column } from "@/list/ListTable";
import { PosterGrid } from "@/list/PosterGrid";
import { useGridMetrics } from "@/list/useGridMetrics";
import { useLibraryList } from "@/list/useLibraryList";
import { useRouteView } from "@/list/useRouteView";

import {
  DEFAULT_POSTER_OPTIONS, IndexViewToggle, OVERVIEW_ROW_HEIGHT, POSTER_GAP, POSTER_PADDING, POSTER_WIDTHS, PosterOptionsMenu, posterRowHeight,
  type IndexView, type PosterOptions,
} from "./IndexViews";
import { CoverArt } from "./CoverArt";
import { CustomFilterMenu } from "./CustomFilterMenu";
import { Poster } from "./Poster";

type Person = components["schemas"]["PersonEntry"];
type Role = MediaTypeInfo["roles"][number];
type SortKey = "name" | "works" | "latest" | "added";

const FILTER_KEYS: readonly string[] = ["role", "monitored", "q", "where"];
const SORT_LABEL: Record<SortKey, string> = { name: "Name", works: "Works", latest: "Newest work", added: "Added" };
/** "everyone" in the address: no role chosen, which for a library with a lead role is not its default. */
const EVERYONE = "all";

/** The roles a list offers: the library's media type's, or every type's once each for the list of all libraries. */
function rolesOf(types: readonly MediaTypeInfo[], mediaType: string | undefined): Role[] {
  const roles: Role[] = [];
  for (const type of types) {
    if (mediaType && type.key !== mediaType) continue;
    for (const role of type.roles) if (!roles.some((seen) => seen.key === role.key)) roles.push(role);
  }
  return roles;
}

/** What a person's tile says about how complete their works are, when anything of theirs is wanted. */
function statusOf(person: Person): AlbumStatus | undefined {
  if (person.downloading) return "downloading";
  if (person.wanted === 0) return undefined;
  return person.complete >= person.wanted ? "complete" : "incomplete";
}

/** People of one library, or of every library the viewer may read: the same wall of faces, list and overview for
 *  every media type, its role chips from the type's own roles. */
export function PeopleIndex() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const library = useLibrary(categoryId);
  const types = useMediaTypes();
  const type = types.find((entry) => entry.key === library?.media_type);
  const roles = rolesOf(types, categoryId ? library?.media_type : undefined);
  // A library opens on its lead role (Artists, Cast); the list of every library on everyone.
  const lead = categoryId ? (roles.find((role) => role.lead) ?? roles[0]) : undefined;
  const view = useRouteView(FILTER_KEYS);
  const chosen = view.query.filters.role || (lead ? lead.key : EVERYONE);
  const role = chosen === EVERYONE ? "" : chosen;
  const scope = categoryId ?? "all";
  const [storedMode, setMode] = usePreference<IndexView>(`people.view.${scope}`, "poster");
  const mode: IndexView = storedMode === "list" || storedMode === "overview" ? storedMode : "poster";
  const sort: SortKey = (["works", "latest", "added"] as const).find((key) => key === view.query.sort) ?? "name";
  const direction = view.query.direction;
  const [storedOptions, setOptions] = usePreference<PosterOptions>("posters.options", DEFAULT_POSTER_OPTIONS);
  const options = { ...DEFAULT_POSTER_OPTIONS, ...storedOptions };
  const frameRef = useRef<HTMLDivElement>(null);
  const metrics = useGridMetrics(frameRef, POSTER_WIDTHS[options.size], POSTER_GAP, POSTER_PADDING);
  const works = categoryId ? (type?.work.many ?? "works").toLowerCase() : "works";
  const workOne = categoryId ? (type?.work.one ?? "work").toLowerCase() : "work";

  const [typed, setTyped] = useState(view.query.filters.q ?? "");
  useEffect(() => {
    const timer = setTimeout(() => {
      if (typed.trim() !== (view.query.filters.q ?? "")) view.setQuery({ ...view.query, filters: { ...view.query.filters, q: typed.trim() } });
    }, 250);
    return () => clearTimeout(timer);
  }, [typed, view]);

  const filterFields = useMemo<readonly FilterField[]>(
    () => [
      { key: "name", label: "Name", kind: "text" },
      { key: "role", label: "Role", kind: "enum", choices: roles.map((entry) => [entry.key, entry.label] as [string, string]) },
      { key: "works", label: `${type?.work.many ?? "Works"} here`, kind: "number" },
      { key: "wanted", label: "Wanted", kind: "number" },
      { key: "complete", label: "Complete", kind: "number" },
      { key: "monitored", label: "Followed", kind: "bool" },
      { key: "photo", label: "Has a photo", kind: "bool" },
      { key: "country", label: "Country", kind: "text" },
    ],
    [roles, type],
  );

  const list = useLibraryList<Person>(
    {
      name: `people-${scope}-${role}`,
      idOf: (person) => person.id,
      rowHeight: mode === "poster" ? posterRowHeight(options, metrics.cellWidth) : mode === "overview" ? OVERVIEW_ROW_HEIGHT / 1.6 : 44,
      itemsPerRow: mode === "poster" ? metrics.columns : 1,
      fetchPage: async ({ cursor, query, signal }) => {
        const filters = query.filters;
        const { data, error } = await api.GET("/people", {
          params: {
            query: {
              category: categoryId ? Number(categoryId) : undefined,
              role: role || undefined,
              q: filters.q || undefined,
              monitored: filters.monitored ? filters.monitored === "true" : undefined,
              sort,
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

  const label = (key: string) => roles.find((entry) => entry.key === key)?.label ?? key.replace(/_/g, " ");
  const linkOf = (person: Person) =>
    categoryId
      ? { to: "/library/$categoryId/people/$personId" as const, params: { categoryId, personId: String(person.id) } }
      : { to: "/people/$id" as const, params: { id: String(person.id) } };
  const what = (person: Person) => person.character ?? person.roles.map(label).join(", ");
  const count = (person: Person) => `${person.works} ${person.works === 1 ? workOne : works}`;
  const setFilter = (key: string, value: string) => view.setQuery({ ...view.query, filters: { ...view.query.filters, [key]: value } });
  const monitored = view.query.filters.monitored ?? "";
  const title = categoryId ? (type?.lead_people.many || "People") : "People";
  const canAdd = categoryId && library && (library.permission === "request" || library.permission === "manage") && lead?.lead;

  const columns: readonly Column<Person>[] = [
    {
      key: "name", header: "Name", sortable: true, width: "minmax(12rem,2fr)",
      render: (person) => (
        <Link {...linkOf(person)} className="flex items-center gap-2 hover:underline" onClick={(event) => event.stopPropagation()}>
          <span className="block size-7 shrink-0 overflow-hidden rounded-full border border-border-subtle bg-surface">
            {person.image_url ? <img src={remoteImage(person.image_url, "thumb")} alt="" loading="lazy" className="size-full object-cover" /> : <User aria-hidden className="m-1 size-5 text-ink-muted" />}
          </span>
          {person.name}
        </Link>
      ),
    },
    { key: "roles", header: "Roles", width: "minmax(8rem,1.5fr)", render: (person) => person.roles.map(label).join(", ") },
    { key: "character", header: "As", width: "minmax(8rem,1.5fr)", render: (person) => person.character ?? "" },
    { key: "works", header: type?.work.many ?? "Works", sortable: true, width: "6rem", render: (person) => person.works },
    { key: "latest", header: "Newest", sortable: true, width: "6rem", render: (person) => person.latest ?? "" },
    { key: "followed", header: "Followed", width: "6rem", render: (person) => (person.monitored ? "Yes" : "") },
  ];

  return (
    <div ref={frameRef} className="flex h-full flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border-subtle px-4 py-2">
        <h2 className="mr-2 text-sm font-medium">{title}</h2>
        {roles.length > 1 &&
          [...roles.map((entry) => ({ key: entry.key, label: entry.label })), { key: EVERYONE, label: "Everyone" }].map((entry) => (
            <button
              key={entry.key}
              type="button"
              aria-pressed={entry.key === chosen}
              onClick={() => setFilter("role", entry.key === (lead?.key ?? EVERYONE) ? "" : entry.key)}
              className={`rounded-full border px-3 py-1 text-xs ${
                entry.key === chosen ? "border-accent bg-accent/10 text-ink" : "border-border-subtle text-ink-muted hover:text-ink"
              }`}
            >
              {entry.label}
            </button>
          ))}
        <div className="ml-auto flex flex-wrap items-center gap-2 text-xs">
          {canAdd && (
            <Link
              to="/library/$categoryId/discover"
              params={{ categoryId }}
              className="flex items-center gap-1 rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-ink"
            >
              <Plus aria-hidden className="size-3.5" />
              Add
            </Link>
          )}
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
              aria-label="Filter people by name"
              className="w-40 bg-transparent text-ink outline-none"
            />
          </label>
          <CustomFilterMenu fields={filterFields} storageKey={`people.${scope}`} where={view.query.filters.where ?? ""} onChange={(where) => setFilter("where", where)} />
          <label className="flex items-center gap-1 text-ink-muted">
            Sort
            <select
              value={sort}
              onChange={(event) => view.setQuery({ ...view.query, sort: event.target.value, direction: event.target.value === "name" ? "asc" : "desc" })}
              className="rounded-md border border-border-subtle bg-surface px-1.5 py-1 text-ink"
            >
              {(Object.keys(SORT_LABEL) as SortKey[]).map((key) => (
                <option key={key} value={key}>
                  {key === "works" ? (type?.work.many ?? SORT_LABEL[key]) : SORT_LABEL[key]}
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
              ? "Nobody is followed yet. Open a person and choose Follow to have their new works added for you."
              : monitored === "false"
                ? "Everyone listed here is followed."
                : "Nobody here yet. People arrive with the works that credit them: as a library is scanned and its metadata fetched."}
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
                    <Link {...linkOf(person)} className="flex h-full items-center gap-4 border-b border-border-subtle hover:bg-surface">
                      <span className="block size-16 shrink-0 overflow-hidden rounded-full border border-border-subtle bg-surface">
                        {person.image_url ? (
                          <img src={remoteImage(person.image_url, "small")} alt="" loading="lazy" className="size-full object-cover" />
                        ) : (
                          <CoverArt subject="people" workId={person.id} title={person.name} size="small" className="size-full object-cover" />
                        )}
                      </span>
                      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                        <span className="truncate text-base font-medium">{person.name}</span>
                        <span className="truncate text-sm text-ink-muted">
                          {person.roles.map(label).join(", ")}
                          {person.character ? ` · as ${person.character}` : ""}
                        </span>
                        <span className="text-xs text-ink-muted">
                          {count(person)}
                          {person.wanted > 0 ? ` · ${person.complete} of ${person.wanted} complete` : ""}
                          {person.monitored ? " · followed" : ""}
                        </span>
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
                imageId={person.image_url ? null : person.id}
                imageSrc={person.image_url ? remoteImage(person.image_url, "small") : undefined}
                title={person.name}
                subtitle={[what(person), count(person)].filter(Boolean).join(" · ")}
                link={linkOf(person)}
                round
                showTitle={options.showTitle}
                status={options.showAvailability ? statusOf(person) : undefined}
                presentCount={person.complete}
                requiredCount={person.wanted}
              />
            )}
          />
        )}
      </div>
    </div>
  );
}
