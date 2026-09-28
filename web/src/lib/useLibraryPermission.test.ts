import { describe, expect, it } from "vitest";

import type { components } from "@/api/schema";
import { canRequestIn } from "./useLibraryPermission";

type Library = components["schemas"]["Library"];

const library = (id: number, mediaType: string, permission: Library["permission"]): Library => ({
  id, media_type: mediaType, name: `Library ${id}`, levels: [], permission,
});

describe("canRequestIn", () => {
  const libraries = [library(1, "music", "read"), library(2, "music", "request"), library(3, "movies", "manage")];

  it.each([
    ["a read-only library", (l: Library) => l.id === 1, false],
    ["a request library", (l: Library) => l.id === 2, true],
    ["manage includes request", (l: Library) => l.id === 3, true],
    ["any music library", (l: Library) => l.media_type === "music", true],
    ["no such library", (l: Library) => l.id === 9, false],
  ])("%s", (_, which, want) => {
    expect(canRequestIn(libraries, which)).toBe(want);
  });
});
