import { describe, expect, it } from "vitest";

import { starterProfile } from "./QualityStep";
import { buildTasks, foldersOf, syncOptions } from "./runSetup";
import { DEFAULT_CHOICES, EMPTY_WIZARD, stepsOf, type WizardConnection, type WizardLibrary, type WizardState } from "./wizardState";

const library: WizardLibrary = { key: "arr-3", name: "Movies", mediaType: "movie", kind: "", folders: [], starter: "arr" };
const connection: WizardConnection = {
  instanceId: 3, app: "radarr", name: "Radarr", baseUrl: "http://radarr:7878", libraryKey: "arr-3", push: false,
  folderMaps: [{ arrPath: "/movies", here: "/mnt/movies/" }, { arrPath: "/4k", here: "/4k" }, { arrPath: "/dup", here: "/mnt/movies" }],
  choices: { ...DEFAULT_CHOICES },
};
const prowlarr: WizardConnection = { instanceId: 9, app: "prowlarr", name: "Prowlarr", baseUrl: "http://prowlarr:9696", folderMaps: [], choices: { ...DEFAULT_CHOICES }, push: false };
const migrating: WizardState = { ...EMPTY_WIZARD, path: "migrate", languages: ["en"], libraries: [library], connections: [prowlarr, connection] };

describe("the library folders of a migrating connection", () => {
  it("are the places its folders are here, once each, without a trailing slash", () => {
    expect(foldersOf(migrating, library)).toEqual(["/mnt/movies", "/4k"]);
  });
  it("on the fresh path are the folders typed, those that are not absolute dropped", () => {
    expect(foldersOf(EMPTY_WIZARD, { ...library, key: "x", folders: ["/a", "", "relative"] })).toEqual(["/a"]);
  });
});

describe("syncOptions", () => {
  it("rewrites only the folders that differ and carries the library and the extras", () => {
    expect(syncOptions(connection, 12)).toEqual({
      mode: "add", monitored: true, unmonitored: false, albums: "all", limit: 0,
      indexers: true, download_clients: true, import_lists: true,
      path_rewrites: [{ from: "/movies", to: "/mnt/movies/" }, { from: "/dup", to: "/mnt/movies" }],
      new_folders_library_id: 12,
    });
  });
});

describe("buildTasks", () => {
  it("makes the library before the import, and Prowlarr's indexers before the import that would find them keyless", () => {
    const ids = buildTasks(migrating).map((task) => task.id);
    expect(ids).toEqual(["languages", "library:arr-3", "prowlarr:9", "link:3", "import:3"]);
    const imported = buildTasks(migrating).find((task) => task.id === "import:3");
    expect(imported?.needs).toEqual(["link:3", "prowlarr:9"]);
  });
  it("sets a starter quality only on a library it made", () => {
    const fresh: WizardState = { ...EMPTY_WIZARD, libraries: [{ ...library, key: "a", starter: "space" }, { ...library, key: "b", starter: "space", libraryId: 4 }] };
    expect(buildTasks(fresh).map((task) => task.id)).toEqual(["library:a", "quality:a", "library:b"]);
  });
});

describe("starterProfile", () => {
  const profiles = [
    { id: 1, name: "Lossy 320", media_type: "music", rules: "{}", upgrade: "{}" },
    { id: 2, name: "Space conscious", media_type: "music", rules: "{}", upgrade: "{}" },
  ];
  it("finds the shipped profile a starter means, and none for the *Arr's or the default", () => {
    expect(starterProfile(profiles, "music", "balanced")?.id).toBe(1);
    expect(starterProfile(profiles, "music", "arr")).toBeUndefined();
    expect(starterProfile(profiles, "music", "keep")).toBeUndefined();
    expect(starterProfile(profiles, "unknown", "best")).toBeUndefined();
  });
});

describe("the steps", () => {
  it("differ by path and are only the start before one is chosen", () => {
    expect(stepsOf(undefined)).toEqual(["start"]);
    expect(stepsOf("migrate")).not.toContain("libraries");
    expect(stepsOf("fresh")).toContain("tools");
  });
});
