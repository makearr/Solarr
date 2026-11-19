import { describe, expect, it } from "vitest";

import type { components } from "@/api/schema";
import { canAddIn } from "./useLibraryPermission";

type Library = components["schemas"]["Library"];

const library = (id: number, mediaType: string, permission: Library["permission"]): Library => ({
  id, media_type: mediaType, name: `Library ${id}`, levels: [], permission,
});

describe("canAddIn", () => {
  const libraries = [library(1, "music", "read"), library(2, "music", "add"), library(4, "music", "request"), library(3, "movies", "manage")];

  it.each([
    ["a read-only library", (l: Library) => l.id === 1, false],
    ["an add library", (l: Library) => l.id === 2, true],
    ["a request-only library", (l: Library) => l.id === 4, false],
    ["manage includes add", (l: Library) => l.id === 3, true],
    ["any music library", (l: Library) => l.media_type === "music", true],
    ["no such library", (l: Library) => l.id === 9, false],
  ])("%s", (_, which, want) => {
    expect(canAddIn(libraries, which)).toBe(want);
  });
});
