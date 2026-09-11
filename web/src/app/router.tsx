import {
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  redirect,
} from "@tanstack/react-router";

import { api } from "@/api/client";

import { Activity } from "./Activity";
import { ArrImport } from "./ArrImport";
import { Dashboard } from "./Dashboard";
import { Discover } from "./Discover";
import { DownloadFolders } from "./DownloadFolders";
import { Import } from "./Import";
import { ImportLists } from "./ImportLists";
import { Library } from "./Library";
import { LibraryConfigPlaceholder } from "./LibraryConfigPlaceholder";
import { LibraryLevelList } from "./LibraryLevelList";
import { Moves } from "./Moves";
import { Packs } from "./Packs";
import { People } from "./People";
import { PersonDetail } from "./PersonDetail";
import { RootFolders } from "./RootFolders";
import { Settings } from "./Settings";
import { Shell } from "./Shell";
import { defaultLevelKey } from "./Sidebar";
import { Unidentified } from "./Unidentified";
import { WorkDetail } from "./WorkDetail";

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
        throw redirect({ to: "/library/$categoryId/albums", params: { categoryId } });
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
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/albums", component: Library }),
  // A real screen (unlike quality/naming/language below), so it gets its own
  // static route rather than falling through the generic $section catch-all.
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/config/import-lists",
    component: ImportLists,
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/library/$categoryId/config/$section",
    component: LibraryConfigPlaceholder,
  }),
  // Registered ahead of the $level catch-all below, or "discover" would
  // resolve as an (invalid) level key instead of this screen.
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/discover", component: Discover }),
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/$level", component: LibraryLevelList }),
  createRoute({ getParentRoute: () => rootRoute, path: "/works/$id", component: WorkDetail }),
  createRoute({ getParentRoute: () => rootRoute, path: "/people", component: People }),
  createRoute({ getParentRoute: () => rootRoute, path: "/people/$id", component: PersonDetail }),
  createRoute({ getParentRoute: () => rootRoute, path: "/unidentified", component: Unidentified }),
  // Storage (todo 4.18): Root folders, Move & drain, Download folders and
  // Import are grouped under one section. /import and /moves redirect
  // rather than 404ing, the same courtesy the old flat /library got.
  createRoute({ getParentRoute: () => rootRoute, path: "/storage/roots", component: RootFolders }),
  createRoute({ getParentRoute: () => rootRoute, path: "/storage/moves", component: Moves }),
  createRoute({ getParentRoute: () => rootRoute, path: "/storage/downloads", component: DownloadFolders }),
  createRoute({ getParentRoute: () => rootRoute, path: "/storage/import", component: Import }),
  createRoute({ getParentRoute: () => rootRoute, path: "/storage/packs", component: Packs }),
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
      throw redirect({ to: "/storage/moves" });
    },
  }),
  createRoute({ getParentRoute: () => rootRoute, path: "/activity", component: Activity }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings", component: Settings }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings/arr", component: ArrImport }),
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
