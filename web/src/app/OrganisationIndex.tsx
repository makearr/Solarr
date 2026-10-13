import { useRef } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Building2 } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { remoteImage } from "@/lib/remoteImage";
import { useLibrary } from "@/lib/useLibrary";
import { useMediaTypes } from "@/lib/useMediaTypes";
import { usePreference } from "@/lib/usePreference";
import { PosterGrid } from "@/list/PosterGrid";
import { useGridMetrics } from "@/list/useGridMetrics";
import { useLibraryList } from "@/list/useLibraryList";
import { useRouteView } from "@/list/useRouteView";
import { Toolbar, ToolbarSearch, ToolbarSelect, ToolbarSort } from "@/ui/Toolbar";

import { DEFAULT_POSTER_OPTIONS, POSTER_GAP, POSTER_PADDING, POSTER_WIDTHS, PosterOptionsMenu, posterRowHeight, type PosterOptions } from "./IndexViews";
import { CreditTile } from "./PersonPage";
import { FollowPanel } from "./FollowPanel";
import { Poster } from "./Poster";

type Organisation = components["schemas"]["OrganisationEntry"];

const ORGANISATION_FILTER_KEYS: readonly string[] = ["q", "monitored"];

/** A library's organisations (networks, labels, studios): most works first, on the same wall as every other level. */
export function OrganisationIndex() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const library = useLibrary(categoryId);
  const type = useMediaTypes().find((entry) => entry.key === library?.media_type);
  const [storedOptions, setOptions] = usePreference<PosterOptions>("posters.options", DEFAULT_POSTER_OPTIONS);
  const options = { ...DEFAULT_POSTER_OPTIONS, ...storedOptions };
  const frameRef = useRef<HTMLDivElement>(null);
  const metrics = useGridMetrics(frameRef, POSTER_WIDTHS[options.size], POSTER_GAP, POSTER_PADDING);
  const view = useRouteView(ORGANISATION_FILTER_KEYS);
  const sort = view.query.sort ?? "works";
  // The address carries a direction only beside a sort: unsorted is the default, most works first.
  const descending = view.query.sort === null || view.query.direction === "desc";
  const setFilter = (key: string, value: string) => view.setQuery({ ...view.query, filters: { ...view.query.filters, [key]: value } });
  const works = (type?.work.many ?? "works").toLowerCase();
  const many = type?.organisation.many || "Organisations";
  const list = useLibraryList<Organisation>(
    {
      name: `organisations-${categoryId}`,
      idOf: (organisation) => organisation.id,
      rowHeight: posterRowHeight({ ...options, showAvailability: false }, metrics.cellWidth),
      itemsPerRow: metrics.columns,
      fetchPage: async ({ cursor, query, signal }) => {
        const { data, error } = await api.GET("/organisations", {
          params: {
            query: {
              category: Number(categoryId),
              q: query.filters.q || undefined,
              monitored: query.filters.monitored ? query.filters.monitored === "true" : undefined,
              sort: sort as "name" | "works" | "latest",
              dir: descending ? "desc" : "asc",
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

  return (
    <div ref={frameRef} className="flex h-full flex-col">
      <Toolbar title={many}>
        <ToolbarSelect label="Show" value={view.query.filters.monitored ?? ""} onChange={(value) => setFilter("monitored", value)}>
          <option value="">All</option>
          <option value="true">Followed</option>
          <option value="false">Not followed</option>
        </ToolbarSelect>
        <ToolbarSearch value={view.query.filters.q ?? ""} onChange={(value) => setFilter("q", value)} placeholder="Name starts with…" label={`Filter ${many.toLowerCase()} by name`} />
        <ToolbarSort
          sort={sort}
          descending={descending}
          choices={[["works", type?.work.many ?? "Works"], ["name", "Name"], ["latest", "Newest"]]}
          onSort={(next) => view.setQuery({ ...view.query, sort: next, direction: next === "name" ? "asc" : "desc" })}
          onTurn={() => view.setQuery({ ...view.query, sort, direction: descending ? "asc" : "desc" })}
        />
        <PosterOptionsMenu options={options} onChange={setOptions} />
      </Toolbar>
      <div className="min-h-0 flex-1">
        {list.isLoading && <p className="p-4 text-sm text-ink-muted">Loading…</p>}
        {list.error !== null && <p className="p-4 text-sm text-danger">These could not be loaded.</p>}
        {!list.isLoading && list.error === null && list.items.length === 0 && (
          <p className="p-4 text-sm text-ink-muted">
            {view.query.filters.q || view.query.filters.monitored ? "None match." : `None known yet: they arrive with the ${works}' metadata.`}
          </p>
        )}
        {list.items.length > 0 && (
          <PosterGrid
            list={list}
            idOf={(organisation) => organisation.id}
            renderPoster={(organisation) => (
              <Poster
                subject="works"
                imageId={null}
                imageSrc={organisation.logo_url ? remoteImage(organisation.logo_url, "small") : undefined}
                contain
                title={organisation.name}
                subtitle={`${organisation.works} ${organisation.works === 1 ? (type?.work.one ?? "work").toLowerCase() : works}`}
                link={{ to: "/library/$categoryId/organisations/$organisationId", params: { categoryId, organisationId: String(organisation.id) } }}
                showTitle={options.showTitle}
              />
            )}
          />
        )}
      </div>
    </div>
  );
}

/** One organisation, with its works in every library the viewer may read and its follow. */
export function OrganisationPage() {
  const params = useParams({ strict: false }) as { id?: string; organisationId?: string; categoryId?: string };
  const id = Number(params.organisationId ?? params.id);
  const from = params.categoryId;
  const types = useMediaTypes();
  const detail = useQuery({
    queryKey: ["organisation", id],
    enabled: Number.isFinite(id),
    queryFn: async () => {
      const { data, error } = await api.GET("/organisations/{id}", { params: { path: { id } } });
      if (error) throw error;
      return data;
    },
  });
  const libraries = useQuery({
    queryKey: ["libraries"],
    queryFn: async () => {
      const { data, error } = await api.GET("/libraries");
      if (error) throw error;
      return data;
    },
  });
  if (detail.isLoading) return <p className="p-4 text-sm text-ink-muted">Loading…</p>;
  if (detail.isError || !detail.data) return <p className="p-4 text-sm text-danger">This could not be found.</p>;

  const organisation = detail.data;
  const sectionIds = [...new Set(organisation.libraries.map((place) => place.category_id))];
  const fromLibrary = libraries.data?.find((library) => String(library.id) === from);
  const fromType = types.find((type) => type.key === fromLibrary?.media_type);
  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-5 p-4">
      {from && (
        <Link
          to="/library/$categoryId/$level"
          params={{ categoryId: from, level: fromLibrary?.levels.find((level) => level.entity === "organisation")?.key ?? "networks" }}
          className="text-xs text-ink-muted hover:text-ink"
        >
          ← {fromType?.organisation.many || "Back"}
        </Link>
      )}
      <div className="flex items-center gap-4">
        {organisation.logo_url ? (
          <img src={remoteImage(organisation.logo_url, "thumb")} alt="" className="max-h-14 max-w-40 object-contain" />
        ) : (
          <Building2 aria-hidden className="size-10 text-ink-muted" />
        )}
        <div>
          <h1 className="text-xl font-semibold">{organisation.name}</h1>
          <p className="text-sm text-ink-muted">{[organisation.country?.toUpperCase(), organisation.kind].filter(Boolean).join(" · ")}</p>
        </div>
      </div>
      <FollowPanel subject="organisation" subjectId={organisation.id} libraryIds={sectionIds} />
      {sectionIds.map((categoryId) => {
        const library = libraries.data?.find((entry) => entry.id === categoryId);
        const type = types.find((entry) => entry.key === library?.media_type);
        const credits = organisation.credits.filter((credit) => credit.category_id === categoryId);
        const label = (role: string) => (role === "network" || role === "label" ? "" : role);
        return (
          <section key={categoryId} className="flex flex-col gap-3">
            <h2 className="text-sm font-semibold">
              {library?.name ?? "Library"}{" "}
              <span className="font-normal text-ink-muted">
                · {credits.length} {((credits.length === 1 ? type?.work.one : type?.work.many) ?? "works").toLowerCase()}
              </span>
            </h2>
            <div className="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-4">
              {credits.map((credit) => (
                <CreditTile key={`${credit.work.id}-${credit.role}`} credit={credit} label={label(credit.role)} />
              ))}
            </div>
          </section>
        );
      })}
    </div>
  );
}
