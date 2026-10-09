import {
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  useParams,
  redirect,
} from "@tanstack/react-router";

import { api } from "@/api/client";

import { Activity } from "./Activity";
import { History } from "./History";
import { Queue } from "./Queue";
import { Notifications } from "./Notifications";
import { Statistics } from "./Statistics";
import { ArrImport } from "./ArrImport";
import { Dashboard } from "./Dashboard";
import { Discover } from "./Discover";
import { DownloadFolders } from "./DownloadFolders";
import { Downloaders } from "./Downloaders";
import { Indexers } from "./Indexers";
import { Import } from "./Import";
import { LibraryFileManagement } from "./LibraryFileManagement";
import { ImportLists } from "./ImportLists";
import { CacheSettingsPage } from "./CacheSettings";
import { Library } from "./Library";
import { LibraryNaming } from "./LibraryNaming";
import { UnknownConfigSection } from "./UnknownConfigSection";
import { LibraryHealth } from "./LibraryHealth";
import { LibraryLevelList } from "./LibraryLevelList";
import { AddSeries } from "./AddSeries";
import { SeasonPass } from "./SeasonPass";
import { SeriesIndex } from "./SeriesIndex";
import { SeriesWanted } from "./SeriesWanted";
import { useLibrary } from "@/lib/useLibrary";
import { Libraries } from "./Libraries";
import { Duplicates } from "./Duplicates";
import { Packs } from "./Packs";
import { OrganisationIndex, OrganisationPage } from "./OrganisationIndex";
import { PeopleIndex } from "./PeopleIndex";
import { PersonPage } from "./PersonPage";
import { QualityProfilesPage } from "./QualityProfileEditor";
import { QualityProfiles } from "./QualityProfiles";
import { RecycleBin } from "./RecycleBin";
import { RootFolders } from "./RootFolders";
import { BlocklistsSettings, LibrariesSettings, MetadataSettingsPage, NetworkSettings, TagsSettings } from "./Settings";
import { Backups } from "./Backups";
import { Calendar } from "./Calendar";
import { Logs } from "./Logs";
import { SchedulerPage } from "./Scheduler";
import { Shell } from "./Shell";
import { defaultLevelKey } from "./Sidebar";
import { Unidentified } from "./Unidentified";
import { UserSettings } from "./Users";
import { Wanted } from "./Wanted";
import { UnidentifiedReview } from "./UnidentifiedReview";
import { WorkPage } from "./work/WorkPage";

/** A redirect to one library's own default level. Not every media type
 *  declares Artists or Albums, so the level is read from the library itself. */
async function redirectToLibrary(categoryId: string) {
  const { data } = await api.GET("/libraries");
  const library = data?.find((entry) => String(entry.id) === categoryId);
  const level = library && defaultLevelKey(library);
  return redirect(
    level ? { to: "/library/$categoryId/$level", params: { categoryId, level } } : { to: "/libraries" },
  );
}

/** Search-and-add is a per-media-type screen: series for a TV library, albums and artists for music. */
function DiscoverForLibrary() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const library = useLibrary(categoryId);
  return library?.media_type === "tv" ? <AddSeries /> : <Discover />;
}

/** Wanted is per media type: missing episodes for a TV library, albums for music. */
function WantedForLibrary() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const library = useLibrary(categoryId);
  return library?.media_type === "tv" ? <SeriesWanted /> : <Wanted />;
}

/** A level of people or organisations is one list for every media type; works are still per type (R1.6). */
function LevelForLibrary() {
  const { categoryId, level } = useParams({ strict: false }) as { categoryId: string; level: string };
  const library = useLibrary(categoryId);
  const entity = library?.levels.find((entry) => entry.key === level)?.entity;
  if (entity === "person") return <PeopleIndex />;
  if (entity === "organisation") return <OrganisationIndex />;
  if (library?.media_type === "tv") {
    if (level === "series") return <SeriesIndex />;
    if (level === "seasonpass") return <SeasonPass />;
  }
  return <LibraryLevelList />;
}

const rootRoute = createRootRoute({
  component: () => (
    <Shell>
      <Outlet />
    </Shell>
  ),
});

// Code-based rather than file-based: a generated route tree is a fifth kind of
// generated artefact to keep in step, and the route count here does not earn it.
const routes = [
  createRoute({ getParentRoute: () => rootRoute, path: "/", component: Dashboard }),
  // The old flat /library?category_id=X (ADR-54 replaces it with a per-library
  // tree): redirected, never 404ing, to the same category's Albums level, or
  // (an aggregate dashboard tile has no single category) the first library
  // the caller can see -- there is no unfiltered browse view any more.
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library",
    beforeLoad: async ({ search }) => {
      const categoryId = (search as Record<string, unknown>).category_id;
      if (typeof categoryId === "string") {
        throw await redirectToLibrary(categoryId);
      }
      const { data } = await api.GET("/libraries");
      const first = data?.[0];
      const level = first && defaultLevelKey(first);
      throw redirect(
        first && level
          ? { to: "/library/$categoryId/$level", params: { categoryId: String(first.id), level } }
          : { to: "/" },
      );
    },
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/libraries", component: Libraries }),
  createRoute({ getParentRoute: () => rootRoute, path: "/calendar", component: Calendar }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/albums", component: Library }),
  // Real screens (unlike naming below), so each gets its own static route
  // rather than falling through the generic $section catch-all. Quality and
  // language profiles are global, not per-library (todo 4.3, 2026-09-12) --
  // reached from here the same way Packs is, but the same list regardless
  // of which category's config page got you here.
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/config/import-lists",
    component: ImportLists,
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/config/quality",
    component: QualityProfiles,
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/config/language",
    beforeLoad: () => {
      throw redirect({ to: "/quality-profiles" });
    },
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/config/naming",
    component: LibraryNaming,
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/config/file-management",
    component: LibraryFileManagement,
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/config/$section",
    component: UnknownConfigSection,
  }),
  // Registered ahead of the $level catch-all below, or these would each
  // resolve as an (invalid) level key instead of their own screen.
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/discover", component: DiscoverForLibrary }),
  // Overview removed 2026-09-13 (the user's own word: it showed nothing the
  // level views do not). Redirected rather than 404ing, the same courtesy the
  // old flat /library got.
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/overview",
    beforeLoad: async ({ params }) => {
      throw await redirectToLibrary((params as { categoryId: string }).categoryId);
    },
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/wanted", component: WantedForLibrary }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/statistics", component: Statistics }),
  // Packs moved here from Storage 2026-09-11 (a pack is one library's own
  // provenance fact, ADR-50, not a storage concern) -- still the same
  // global /packs data underneath (no category_id on pack itself, only on
  // its own session, which can be null), so every library's Packs page
  // shows every pack today. Narrowing this is a real follow-up, not done here.
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/packs", component: Packs }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/duplicates", component: Duplicates }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/import", component: Import }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/activity", component: Activity }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/works/$workId", component: WorkPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/works/$workId/$tab", component: WorkPage }),
  // A series had its own page until R1.6; old links land on the work page.
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/series/$seriesId",
    beforeLoad: ({ params }) => {
      const { categoryId, seriesId } = params as { categoryId: string; seriesId: string };
      throw redirect({ to: "/library/$categoryId/works/$workId", params: { categoryId, workId: seriesId } });
    },
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/people/$personId", component: PersonPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/organisations/$organisationId", component: OrganisationPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/networks/$networkId", component: OrganisationPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/organisations/$id", component: OrganisationPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/$level", component: LevelForLibrary }),
  // A work linked without its library (queue, history, search) opens inside the first library that lists it.
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/works/$id",
    beforeLoad: async ({ params }) => {
      const id = (params as { id: string }).id;
      const { data } = await api.GET("/works/{id}", { params: { path: { id: Number(id) } } });
      const library = data?.library_ids?.[0];
      if (library !== undefined) throw redirect({ to: "/library/$categoryId/works/$workId", params: { categoryId: String(library), workId: id } });
    },
    component: WorkPage,
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/works/$id/$tab", component: WorkPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/people", component: PeopleIndex }),
  createRoute({ getParentRoute: () => rootRoute, path: "/people/$id", component: PersonPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/unidentified", component: Unidentified }),
  createRoute({ getParentRoute: () => rootRoute, path: "/unidentified/$mediaObjectId", component: UnidentifiedReview }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/health", component: LibraryHealth }),
  // Storage (todo 4.18): Root folders, Move & drain, Download folders and
  // Import are grouped under one section. /import and /moves redirect
  // rather than 404ing, the same courtesy the old flat /library got.
  createRoute({ getParentRoute: () => rootRoute, path: "/storage/roots", component: RootFolders }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/storage/moves",
    beforeLoad: () => {
      throw redirect({ to: "/storage/roots" });
    },
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/storage/downloads", component: DownloadFolders }),
  createRoute({ getParentRoute: () => rootRoute, path: "/storage/import", component: Import }),
  createRoute({ getParentRoute: () => rootRoute, path: "/storage/recycle-bin", component: RecycleBin }),
  // Packs moved to each library 2026-09-11; redirected rather than 404ing,
  // same as /import and /moves below.
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/storage/packs",
    beforeLoad: async () => {
      const { data } = await api.GET("/libraries");
      const first = data?.[0];
      throw redirect(
        first ? { to: "/library/$categoryId/packs", params: { categoryId: String(first.id) } } : { to: "/" },
      );
    },
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/import",
    beforeLoad: () => {
      throw redirect({ to: "/storage/import" });
    },
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/moves",
    beforeLoad: () => {
      throw redirect({ to: "/storage/roots" });
    },
  }),
  // Connections (stage 5, ADR-54/todo 5.2): indexers and download clients so
  // far. *Arr apps stay at /settings/arr for now (their own relocation is a
  // separate, deliberately deferred item) and metadata sources are not
  // selectable per library yet either.
  createRoute({ getParentRoute: () => rootRoute, path: "/connections/indexers", component: Indexers }),
  createRoute({ getParentRoute: () => rootRoute, path: "/connections/downloaders", component: Downloaders }),
  createRoute({ getParentRoute: () => rootRoute, path: "/connections/arr", component: ArrImport }),
  // Activity holds History and Jobs, as Lidarr's holds Queue and History (2026-09-25).
  createRoute({ getParentRoute: () => rootRoute, path: "/quality-profiles", component: QualityProfilesPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/activity/queue", component: Queue }),
  createRoute({ getParentRoute: () => rootRoute, path: "/activity/history", component: History }),
  createRoute({ getParentRoute: () => rootRoute, path: "/activity/notifications", component: Notifications }),
  createRoute({ getParentRoute: () => rootRoute, path: "/activity/jobs", component: Activity }),
  // Settings split into General/User/Scheduler/Cache 2026-09-11 (asked for
  // twice before this) -- /settings itself now just redirects to the first
  // of the four, the same courtesy the old flat /library got.
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/settings",
    beforeLoad: () => {
      throw redirect({ to: "/settings/libraries" });
    },
  }),
  // General was split on the 2026-09-19 review; old links land on the part they meant.
  ...[
    ["/settings/general", "/settings/libraries"],
    ["/settings/blocklist", "/settings/blocklists"],
    ["/settings/arr", "/connections/arr"],
    ["/activity", "/activity/queue"],
  ].map(([from, to]) =>
    createRoute({
      getParentRoute: () => rootRoute,
      path: from!,
      beforeLoad: () => {
        throw redirect({ to: to! });
      },
    }),
  ),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/libraries", component: LibrariesSettings }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/blocklists", component: BlocklistsSettings }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/tags", component: TagsSettings }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/network", component: NetworkSettings }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/metadata", component: MetadataSettingsPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/backups", component: Backups }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/logs", component: Logs }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/user", component: UserSettings }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/scheduler", component: SchedulerPage }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/cache", component: CacheSettingsPage }),
];

export const router = createRouter({
  routeTree: rootRoute.addChildren(routes),
  defaultPreload: "intent",
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
