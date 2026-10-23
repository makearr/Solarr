import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";

// One hook per resource many screens read whole, so each has one query key and is cached once across the app.

export const useLibraries = () => useQuery({ queryKey: ["libraries"], queryFn: () => ok(api.GET("/libraries")) });

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

/** The whole tag tree, every taxonomy's and the free labels; screens narrow it themselves. */
export const useTagTree = () => useQuery({ queryKey: ["tags"], queryFn: () => ok(api.GET("/tags")) });
