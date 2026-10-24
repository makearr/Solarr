import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";

// One hook per resource many screens read whole, so each has one query key and is cached once across the app.

type Library = components["schemas"]["Library"];

/** A library, or a further media type of one (library_id names the library it belongs to). */
export type LibraryEntry = Library & { library_id?: number };

const fetchLibraries = () => ok(api.GET("/libraries"));

/** Every library and every further media type of one, each with its own id and levels, as most screens want them. */
export const useLibraries = () =>
  useQuery({
    queryKey: ["libraries"],
    queryFn: fetchLibraries,
    select: (libraries): LibraryEntry[] =>
      libraries.flatMap((library) => [library, ...(library.members ?? []).map((member) => ({ ...member, library_id: library.id }))]),
  });

/** The libraries as the tree shows them: one entry each, its further media types inside (members). */
export const useLibraryTree = () => useQuery({ queryKey: ["libraries"], queryFn: fetchLibraries });

export const useCategories = () => useQuery({ queryKey: ["categories"], queryFn: () => ok(api.GET("/categories")) });

export const useRootFolders = () => useQuery({ queryKey: ["rootfolders"], queryFn: () => ok(api.GET("/rootfolders")) });

export const useDownloadFolders = () =>
  useQuery({ queryKey: ["downloadfolders"], queryFn: () => ok(api.GET("/downloadfolders")) });

export const useQualityProfiles = () =>
  useQuery({ queryKey: ["qualityProfiles"], queryFn: () => ok(api.GET("/qualityprofiles")) });

export const useLanguageProfiles = () =>
  useQuery({ queryKey: ["languageProfiles"], queryFn: () => ok(api.GET("/languageprofiles")) });

export const useImportSettings = () =>
  useQuery({ queryKey: ["import-settings"], queryFn: () => ok(api.GET("/import/settings")) });

export const useMetadataSettings = () =>
  useQuery({ queryKey: ["metadata-settings"], queryFn: () => ok(api.GET("/metadata/settings")) });

export const useUserDefaults = () => useQuery({ queryKey: ["user-defaults"], queryFn: () => ok(api.GET("/settings/users")) });

/** The whole tag tree, every genre tree's and the free labels; screens narrow it themselves. */
export const useTagTree = () => useQuery({ queryKey: ["tags"], queryFn: () => ok(api.GET("/tags")) });
