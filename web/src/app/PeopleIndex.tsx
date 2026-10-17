import { LetterRail } from "@/ui/LetterRail";
import { useMemo, useRef } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { User } from "lucide-react";

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
import { Toolbar, ToolbarAdd, ToolbarSearch, ToolbarSelect, ToolbarSort } from "@/ui/Toolbar";

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

const FILTER_KEYS: readonly string[] = ["role", "monitored", "q", "where", "from"];
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
  // A poster library (movies, series) opens on its cast; an album one on its artists.
  const castFirst = type?.work_image === "poster" ? roles.find((role) => role.character) : undefined;
  const lead = categoryId ? (castFirst ?? roles.find((role) => role.lead) ?? roles[0]) : undefined;
  const view = useRouteView(FILTER_KEYS);
  const chosen = view.query.filters.role || (lead ? lead.key : EVERYONE);
  // The coverage bar is the lead role's, as Lidarr's artists carry one; a wall of cast carries none.
  const leadList = Boolean(lead?.lead && chosen === lead.key);
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
      rowHeight:
        mode === "poster"
          ? posterRowHeight({ ...options, showAvailability: options.showAvailability && leadList }, metrics.cellWidth)
          : mode === "overview"
            ? OVERVIEW_ROW_HEIGHT / 1.6
            : 44,
      itemsPerRow: mode === "poster" ? metrics.columns : 1,
      fetchPage: async ({ cursor, query, signal }) => {
        const filters = query.filters;
        const { data, error } = await api.GET("/people", {
          params: {
            query: {
              category: categoryId ? Number(categoryId) : undefined,
              role: role || undefined,
              q: filters.q || undefined,
              from: filters.from || undefined,
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
  // What someone does across the list's works; a character name says nothing outside the one work it belongs to.
  const what = (person: Person) => person.roles.map(label).join(", ") || person.character || "";
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
      <Toolbar
        title={title}
        chips={
          roles.length > 1 &&
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
          ))
        }
      >
        <ToolbarSelect label="Show" value={monitored} onChange={(value) => setFilter("monitored", value)}>
          <option value="">Everyone</option>
          <option value="true">Followed</option>
          <option value="false">Not followed</option>
        </ToolbarSelect>
        <ToolbarSearch value={view.query.filters.q ?? ""} onChange={(value) => setFilter("q", value)} placeholder="Name starts with…" label="Filter people by name" />
        <CustomFilterMenu fields={filterFields} storageKey={`people.${scope}`} where={view.query.filters.where ?? ""} onChange={(where) => setFilter("where", where)} />
        <ToolbarSort
          sort={sort}
          descending={direction === "desc"}
          choices={(Object.keys(SORT_LABEL) as SortKey[]).map((key) => [key, key === "works" ? (type?.work.many ?? SORT_LABEL[key]) : SORT_LABEL[key]] as const)}
          onSort={(next) => view.setQuery({ ...view.query, sort: next, direction: next === "name" ? "asc" : "desc" })}
          onTurn={() => view.setQuery({ ...view.query, sort, direction: direction === "asc" ? "desc" : "asc" })}
        />
        {mode === "poster" && <PosterOptionsMenu options={options} onChange={setOptions} />}
        <IndexViewToggle view={mode} onChange={setMode} />
        {canAdd && <ToolbarAdd categoryId={categoryId} />}
      </Toolbar>
      <div className="relative min-h-0 flex-1 pr-6">
        {sort === "name" && direction !== "desc" && <LetterRail value={view.query.filters.from ?? ""} onChange={(letter) => setFilter("from", letter)} />}
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
                        ) : person.no_artwork ? (
                          <span className="flex size-full items-center justify-center text-xl text-ink-muted">
                            {person.name.trim().charAt(0).toUpperCase() || "?"}
                          </span>
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
                imageId={person.image_url || person.no_artwork ? null : person.id}
                imageSrc={person.image_url ? remoteImage(person.image_url, "small") : undefined}
                title={person.name}
                subtitle={[what(person), count(person)].filter(Boolean).join(" · ")}
                link={linkOf(person)}
                round
                showTitle={options.showTitle}
                status={options.showAvailability && leadList ? statusOf(person) : undefined}
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
