import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";

type Library = components["schemas"]["Library"];

/** Whether any library matching `which` lets the caller add: search, send, follow and monitor. A request-only account can only ask. */
export function canAddIn(libraries: readonly Library[], which: (library: Library) => boolean): boolean {
  return libraries.some(
    (library) => which(library) && (library.permission === "add" || library.permission === "manage"),
  );
}

/** canAddIn over the shared ["libraries"] query; open while it loads -- the server refuses anyway. */
export function useCanAdd(which: (library: Library) => boolean): boolean {
  const libraries = useQuery({
    queryKey: ["libraries"],
    queryFn: () => ok(api.GET("/libraries")),
  });
  return libraries.data === undefined || canAddIn(libraries.data, which);
}
