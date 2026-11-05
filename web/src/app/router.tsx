import {
  createRootRoute,
  createRoute,
  createRouter,
  lazyRouteComponent,
  Outlet,
  useParams,
  redirect,
} from "@tanstack/react-router";

import { api } from "@/api/client";
import { t } from "@/lib/i18n";
import { EmptyState } from "@/ui/EmptyState";

import { Activity } from "./Activity";
import { History } from "./History";
import { Queue } from "./Queue";
import { Dashboard } from "./Dashboard";
import { Import } from "./Import";
import { Folders } from "./Folders";
import { FranchiseIndex } from "./Franchises";
import { CrossoverIndex } from "./Crossovers";
import { LibraryLevelList } from "./LibraryLevelList";
import { useLibrary } from "@/lib/useLibrary";
import { Packs } from "./Packs";
import { OrganisationIndex } from "./OrganisationIndex";
import { PeopleIndex } from "./PeopleIndex";
import { Shell } from "./Shell";
import { defaultLevelKey } from "./Sidebar";
import { WorkIndex } from "./work/WorkIndex";

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

/** A library's level by the entity it lists, one screen per entity for every media type (R1.6). */
function LevelForLibrary() {
  const { categoryId, level } = useParams({ strict: false }) as { categoryId: string; level: string };
  const library = useLibrary(categoryId);
  // Nothing until the library is known: a guess would ask the server for a list this level does not have.
  if (!library) return null;
  if (level === "all" && library.members?.length) return <WorkIndex wholeLibrary />;
  const entity = library.levels.find((entry) => entry.key === level)?.entity;
  if (entity === "person") return <PeopleIndex />;
  if (entity === "organisation") return <OrganisationIndex />;
  if (entity === "work") return <WorkIndex />;
  if (entity === "work_part") return <LibraryLevelList />;
  if (entity === "collection") return <CrossoverIndex />;
  if (entity === "franchise") return <FranchiseIndex />;
  return <EmptyState title={t("level.notFound")} />;
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
  createRoute({ getParentRoute: () => rootRoute, path: "/libraries", component: lazyRouteComponent(() => import("./Libraries"), "Libraries") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/calendar", component: lazyRouteComponent(() => import("./Calendar"), "Calendar") }),
  // Real screens (unlike naming below), so each gets its own static route
  // rather than falling through the generic $section catch-all. Quality and
  // language profiles are global, not per-library (todo 4.3, 2026-09-12) --
  // reached from here the same way Packs is, but the same list regardless
  // of which category's config page got you here.
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/config/import-lists",
    component: lazyRouteComponent(() => import("./ImportLists"), "ImportLists"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/config/quality",
    component: lazyRouteComponent(() => import("./QualityProfiles"), "QualityProfiles"),
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
    component: lazyRouteComponent(() => import("./LibraryNaming"), "LibraryNaming"),
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/config/rules", component: lazyRouteComponent(() => import("./LibraryRules"), "LibraryRules") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/check", component: lazyRouteComponent(() => import("./LibraryCheck"), "LibraryCheck") }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/config/metadata",
    component: lazyRouteComponent(() => import("./LibraryMetadata"), "LibraryMetadata"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/config/file-management",
    component: lazyRouteComponent(() => import("./LibraryFileManagement"), "LibraryFileManagement"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/config/$section",
    component: lazyRouteComponent(() => import("./UnknownConfigSection"), "UnknownConfigSection"),
  }),
  // Registered ahead of the $level catch-all below, or these would each
  // resolve as an (invalid) level key instead of their own screen.
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/collections/$franchiseId", component: lazyRouteComponent(() => import("./Franchises"), "CollectionPage") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/add", component: lazyRouteComponent(() => import("./work/AddAnywhere"), "AddAnywhere") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/discover", component: lazyRouteComponent(() => import("./work/AddWork"), "AddWork") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/add", component: lazyRouteComponent(() => import("./work/AddWork"), "AddWork") }),
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
  // Season pass gave way to the series list's Edit selected (R1.6); old links land on the library.
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/seasonpass",
    beforeLoad: async ({ params }) => {
      throw await redirectToLibrary((params as { categoryId: string }).categoryId);
    },
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/wanted", component: lazyRouteComponent(() => import("./work/WantedPage"), "WantedPage") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/statistics", component: lazyRouteComponent(() => import("./Statistics"), "Statistics") }),
  // Packs moved here from Storage 2026-09-11 (a pack is one library's own
  // provenance fact, ADR-50, not a storage concern) -- still the same
  // global /packs data underneath (no category_id on pack itself, only on
  // its own session, which can be null), so every library's Packs page
  // shows every pack today. Narrowing this is a real follow-up, not done here.
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/packs", component: Packs }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/duplicates", component: lazyRouteComponent(() => import("./Duplicates"), "Duplicates") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/import", component: Import }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/activity", component: Activity }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/works/$workId", component: lazyRouteComponent(() => import("./work/WorkPage"), "WorkPage") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/works/$workId/$tab", component: lazyRouteComponent(() => import("./work/WorkPage"), "WorkPage") }),
  // A series had its own page until R1.6; old links land on the work page.
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/series/$seriesId",
    beforeLoad: ({ params }) => {
      const { categoryId, seriesId } = params as { categoryId: string; seriesId: string };
      throw redirect({ to: "/library/$categoryId/works/$workId", params: { categoryId, workId: seriesId } });
    },
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/people/$personId", component: lazyRouteComponent(() => import("./PersonPage"), "PersonPage") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/organisations/$organisationId", component: lazyRouteComponent(() => import("./OrganisationIndex"), "OrganisationPage") }),
  // Networks had their own address until R1.4; old links land on the organisation page.
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/networks/$networkId",
    beforeLoad: ({ params }) => {
      const { categoryId, networkId } = params as { categoryId: string; networkId: string };
      throw redirect({ to: "/library/$categoryId/organisations/$organisationId", params: { categoryId, organisationId: networkId } });
    },
  }),
  // An organisation linked without its library opens inside the first library that lists it.
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/organisations/$id",
    beforeLoad: async ({ params }) => {
      const id = (params as { id: string }).id;
      const { data } = await api.GET("/organisations/{id}", { params: { path: { id: Number(id) } } });
      const library = data?.libraries[0]?.category_id;
      if (library !== undefined) {
        throw redirect({ to: "/library/$categoryId/organisations/$organisationId", params: { categoryId: String(library), organisationId: id } });
      }
    },
    component: lazyRouteComponent(() => import("./OrganisationIndex"), "OrganisationPage"),
  }),
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
    component: lazyRouteComponent(() => import("./work/WorkPage"), "WorkPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/works/$id/$tab",
    beforeLoad: async ({ params }) => {
      const { id, tab } = params as { id: string; tab: string };
      const { data } = await api.GET("/works/{id}", { params: { path: { id: Number(id) } } });
      const library = data?.library_ids?.[0];
      if (library !== undefined) {
        throw redirect({ to: "/library/$categoryId/works/$workId/$tab", params: { categoryId: String(library), workId: id, tab } });
      }
    },
    component: lazyRouteComponent(() => import("./work/WorkPage"), "WorkPage"),
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/people", component: PeopleIndex }),
  createRoute({ getParentRoute: () => rootRoute, path: "/people/$id", component: lazyRouteComponent(() => import("./PersonPage"), "PersonPage") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/unidentified", component: lazyRouteComponent(() => import("./Unidentified"), "Unidentified") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/unidentified/$mediaObjectId", component: lazyRouteComponent(() => import("./UnidentifiedReview"), "UnidentifiedReview") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/health", component: lazyRouteComponent(() => import("./LibraryHealth"), "LibraryHealth") }),
  // Storage (todo 4.18): Root folders, Move & drain, Download folders and
  // Import are grouped under one section. /import and /moves redirect
  // rather than 404ing, the same courtesy the old flat /library got.
  createRoute({ getParentRoute: () => rootRoute, path: "/storage/folders", component: Folders }),
  // Root and download folders became one Folders tab 2026-10-23.
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/storage/roots",
    beforeLoad: () => {
      throw redirect({ to: "/storage/folders" });
    },
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/storage/downloads",
    beforeLoad: () => {
      throw redirect({ to: "/storage/folders" });
    },
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/crossovers/$id", component: lazyRouteComponent(() => import("./Crossovers"), "CrossoverPage") }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/storage/moves",
    beforeLoad: () => {
      throw redirect({ to: "/storage/folders" });
    },
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/storage/import", component: Import }),
  createRoute({ getParentRoute: () => rootRoute, path: "/storage/duplicates", component: lazyRouteComponent(() => import("./VideoDuplicates"), "VideoDuplicates") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/storage/recycle-bin", component: lazyRouteComponent(() => import("./RecycleBin"), "RecycleBin") }),
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
      throw redirect({ to: "/storage/folders" });
    },
  }),
  // Connections (stage 5, ADR-54/todo 5.2): indexers and download clients so
  // far. *Arr apps stay at /settings/arr for now (their own relocation is a
  // separate, deliberately deferred item) and metadata sources are not
  // selectable per library yet either.
  createRoute({ getParentRoute: () => rootRoute, path: "/connections/indexers", component: lazyRouteComponent(() => import("./Indexers"), "Indexers") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/connections/indexers/add", component: lazyRouteComponent(() => import("./IndexerCatalog"), "IndexerCatalog") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/connections/indexers/add/setup", component: lazyRouteComponent(() => import("./IndexerCatalog"), "IndexerSetup") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/connections/downloaders", component: lazyRouteComponent(() => import("./Downloaders"), "Downloaders") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/connections/arr", component: lazyRouteComponent(() => import("./ArrImport"), "ArrImport") }),
  // Activity holds History and Jobs, as Lidarr's holds Queue and History (2026-09-25).
  createRoute({ getParentRoute: () => rootRoute, path: "/quality-profiles", component: lazyRouteComponent(() => import("./QualityProfileEditor"), "QualityProfilesPage") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/activity/queue", component: Queue }),
  createRoute({ getParentRoute: () => rootRoute, path: "/activity/history", component: History }),
  createRoute({ getParentRoute: () => rootRoute, path: "/activity/notifications", component: lazyRouteComponent(() => import("./Notifications"), "Notifications") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/activity/requests", component: lazyRouteComponent(() => import("./Requests"), "Requests") }),
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
  // About lives outside the router (App answers it signed out too), so only a full load reaches it.
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/settings/about",
    beforeLoad: () => window.location.replace("/about"),
    component: () => null,
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/libraries", component: lazyRouteComponent(() => import("./Settings"), "LibrariesSettings") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/blocklists", component: lazyRouteComponent(() => import("./Settings"), "BlocklistsSettings") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/tags", component: lazyRouteComponent(() => import("./Settings"), "TagsSettings") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/network", component: lazyRouteComponent(() => import("./Settings"), "NetworkSettings") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/player", component: lazyRouteComponent(() => import("./Settings"), "PlayerSettings") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/webui", component: lazyRouteComponent(() => import("./Settings"), "WebUISettings") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/metadata", component: lazyRouteComponent(() => import("./Settings"), "MetadataSettingsPage") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/backups", component: lazyRouteComponent(() => import("./Backups"), "Backups") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/logs", component: lazyRouteComponent(() => import("./Logs"), "Logs") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/user", component: lazyRouteComponent(() => import("./Users"), "UserSettings") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/scheduler", component: lazyRouteComponent(() => import("./Scheduler"), "SchedulerPage") }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/cache", component: lazyRouteComponent(() => import("./CacheSettings"), "CacheSettingsPage") }),
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
