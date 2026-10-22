import { describe, expect, it } from "vitest";

import type { components } from "@/api/schema";

import { t, vocabularyOf } from "./i18n";

const series: components["schemas"]["MediaTypeInfo"] = {
  key: "tv", label: "TV", work: { one: "Series", many: "Series" }, part: { one: "", many: "" }, group: { one: "", many: "" },
  lead_people: { one: "", many: "" }, organisation: { one: "", many: "" }, roles: [], follow_options: [], facets: [],
  monitor_options: [], search_terms: [], work_image: "poster", wanted_grain: "part", add_kinds: [], commits_held_imports: false, duplicates_by_name: false,
  file_settings: [], scan_monitor: { setting: "tv_monitor", label: "", options: [] }, searches_library: false, cover_sources: false, release_filters: false,
};

describe("t", () => {
  it("fills placeholders from the media type's words", () => {
    expect(t("action.searchAll", vocabularyOf({ ...series, work: { one: "Album", many: "Albums" } }))).toBe("Search all albums");
    expect(t("action.searchAll", vocabularyOf(series))).toBe("Search all series");
  });

  it("keeps a placeholder it has no value for, and falls back to neutral words for a type without its own", () => {
    expect(t("work.remove.title", {})).toBe("Remove {title}");
    expect(t("work.noParts", vocabularyOf(series))).toBe("No parts are known yet.");
  });
});
