import { expect, test } from "vitest";

import { CONNECTIONS, entryTab, isAt, librarySections, pageTier, sectionAt, SETTINGS, visibleTabs } from "./sections";

const labels = (tabs: readonly { label: string }[]) => tabs.map((tab) => tab.label);

test("a tab the viewer may not open is absent", () => {
  const [, insights, files, settings] = librarySections("7");
  expect(labels(visibleTabs(insights!, { tier: "normal", administrator: false }))).toEqual(["Statistics"]);
  expect(labels(visibleTabs(insights!, { tier: "advanced", administrator: false }))).toEqual(["Statistics", "Activity"]);
  expect(labels(visibleTabs(files!, { tier: "normal", administrator: false }))).toEqual(["Unidentified", "Defective", "Belongs elsewhere"]);
  expect(labels(visibleTabs(files!, { tier: "advanced", administrator: false }))).toEqual(["Unidentified", "Duplicates", "Defective", "Belongs elsewhere"]);
  expect(labels(visibleTabs(files!, { tier: "expert", administrator: true }))).toEqual([
    "Unidentified", "Import", "Duplicates", "Defective", "Packs", "Belongs elsewhere",
  ]);
  expect(labels(visibleTabs(settings!, { tier: "expert", administrator: false }))).toEqual([]);
  expect(labels(visibleTabs(SETTINGS, { tier: "normal", administrator: false }))).toEqual([]);
  expect(labels(visibleTabs(SETTINGS, { tier: "advanced", administrator: false }))).toEqual(["Unidentified"]);
});

test("the page decides its section, and a library's unidentified files belong to that library", () => {
  expect(sectionAt({ path: "/settings/scheduler" }, undefined)?.key).toBe("settings");
  expect(sectionAt({ path: "/quality-profiles" }, undefined)?.key).toBe("settings");
  expect(sectionAt({ path: "/library/7/config/naming" }, "7")?.key).toBe("library:7:settings");
  expect(sectionAt({ path: "/library/7/duplicates" }, "7")?.key).toBe("library:7:files");
  expect(sectionAt({ path: "/library/7/activity" }, "7")?.key).toBe("library:7:insights");
  expect(sectionAt({ path: "/unidentified", categoryId: "7" }, "7")?.key).toBe("library:7:files");
  expect(sectionAt({ path: "/unidentified" }, undefined)?.key).toBe("settings");
  expect(sectionAt({ path: "/library/7/albums" }, "7")).toBeUndefined();
});

test("a tab is active on its own page and the pages under it", () => {
  const roots = SETTINGS.tabs.find((tab) => tab.label === "Folders")!;
  expect([isAt(roots, { path: "/storage/folders" }), isAt(roots, { path: "/storage/folders/3" }), isAt(roots, { path: "/storage/foldersx" })]).toEqual([
    true, true, false,
  ]);
});

test("a section's entry leads to the first tab the viewer may see", () => {
  expect(entryTab(SETTINGS, { tier: "normal", administrator: true })?.label).toBe("Libraries");
  expect(entryTab(SETTINGS, { tier: "normal", administrator: false })).toBeUndefined();
});

test("a page's tier is its tab's, and normal when no section lists it", () => {
  expect([pageTier("/settings/cache"), pageTier("/storage/folders"), pageTier("/calendar")]).toEqual(["expert", "advanced", "normal"]);
});

test("a library of several types has each type's pages in its sections", () => {
  const kinds = [{ categoryId: "7", label: "Series" }, { categoryId: "9", label: "Movies" }];
  const [wanted] = librarySections("7", kinds);
  expect(labels(wanted!.tabs)).toEqual(["Wanted · Series", "Wanted · Movies"]);
  expect(sectionAt({ path: "/library/9/wanted" }, "7", kinds)?.key).toBe("library:7:wanted");
});

test("connections are one section: four tabs at the normal tier, import lists and webhooks join at the advanced tier", () => {
  expect(labels(visibleTabs(CONNECTIONS, { tier: "normal", administrator: true }))).toEqual(["Download clients", "Indexers", "Arr apps", "Media servers"]);
  expect(sectionAt({ path: "/connections/indexers/add" }, undefined)?.key).toBe("connections");
  expect(labels(visibleTabs(CONNECTIONS, { tier: "advanced", administrator: true }))).toEqual(["Download clients", "Indexers", "Arr apps", "Media servers", "Import lists", "Webhooks"]);
  expect(sectionAt({ path: "/connections/webhooks" }, undefined)?.key).toBe("connections");
});
