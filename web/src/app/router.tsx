import {
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
} from "@tanstack/react-router";

import { Activity } from "./Activity";
import { Dashboard } from "./Dashboard";
import { Import } from "./Import";
import { Library } from "./Library";
import { People } from "./People";
import { PersonDetail } from "./PersonDetail";
import { Settings } from "./Settings";
import { Shell } from "./Shell";
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
  createRoute({ getParentRoute: () => rootRoute, path: "/library", component: Library }),
  createRoute({ getParentRoute: () => rootRoute, path: "/works/$id", component: WorkDetail }),
  createRoute({ getParentRoute: () => rootRoute, path: "/people", component: People }),
  createRoute({ getParentRoute: () => rootRoute, path: "/people/$id", component: PersonDetail }),
  createRoute({ getParentRoute: () => rootRoute, path: "/unidentified", component: Unidentified }),
  createRoute({ getParentRoute: () => rootRoute, path: "/import", component: Import }),
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
