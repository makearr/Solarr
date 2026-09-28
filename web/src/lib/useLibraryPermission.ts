import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type Library = components["schemas"]["Library"];

/** Whether any library matching `which` lets the caller request: search, send and monitor. */
export function canRequestIn(libraries: readonly Library[], which: (library: Library) => boolean): boolean {
  return libraries.some(
    (library) => which(library) && (library.permission === "request" || library.permission === "manage"),
  );
}

/** canRequestIn over the shared ["libraries"] query; open while it loads -- the server refuses anyway. */
export function useCanRequest(which: (library: Library) => boolean): boolean {
  const libraries = useQuery({
    queryKey: ["libraries"],
    queryFn: async () => {
      const { data, error } = await api.GET("/libraries");
      if (error) throw error;
      return data;
    },
  });
  return libraries.data === undefined || canRequestIn(libraries.data, which);
}
