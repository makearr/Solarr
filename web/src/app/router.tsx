import {
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  redirect,
} from "@tanstack/react-router";

import { api } from "@/api/client";

import { Activity } from "./Activity";
import { Dashboard } from "./Dashboard";
import { Import } from "./Import";
import { Library } from "./Library";
import { LibraryLevelList } from "./LibraryLevelList";
import { Moves } from "./Moves";
import { People } from "./People";
import { PersonDetail } from "./PersonDetail";
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
  createRoute({ getParentRoute: () => rootRoute, path: "/library/$categoryId/$level", component: LibraryLevelList }),
  createRoute({ getParentRoute: () => rootRoute, path: "/works/$id", component: WorkDetail }),
  createRoute({ getParentRoute: () => rootRoute, path: "/people", component: People }),
  createRoute({ getParentRoute: () => rootRoute, path: "/people/$id", component: PersonDetail }),
  createRoute({ getParentRoute: () => rootRoute, path: "/unidentified", component: Unidentified }),
  createRoute({ getParentRoute: () => rootRoute, path: "/import", component: Import }),
  createRoute({ getParentRoute: () => rootRoute, path: "/moves", component: Moves }),
  createRoute({ getParentRoute: () => rootRoute, path: "/activity", component: Activity }),
  createRoute({ getParentRoute: () => rootRoute, path: "/settings", component: Settings }),
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
