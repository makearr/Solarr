import type { components } from "@/api/schema";

import { useLibraries } from "./resources";

type Library = components["schemas"]["Library"];

/** One library by its category id, from the shared ["libraries"] query; undefined while it loads. */
export function useLibrary(categoryId: string | undefined): Library | undefined {
  return useLibraries().data?.find((library) => String(library.id) === categoryId);
}
