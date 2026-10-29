import { describe, expect, it } from "vitest";

import type { components } from "@/api/schema";

import { keepsEnabled, toggleChoice } from "./importLists";

const enabled: components["schemas"]["ImportList"] = {
  id: 1, category_id: 2, name: "Mine", type: "anilist_user", options: { username: "someone" }, cadence_hours: 24,
  monitor: true, monitor_option: "", exclusions: "", enabled: true, previewed: true, last_run_added: 0, last_run_failure: "",
};

describe("keepsEnabled", () => {
  it("keeps an enabled list enabled while its type and settings stay as previewed", () => {
    expect(keepsEnabled(enabled, "anilist_user", { username: "someone", statuses: " " })).toBe(true);
  });

  it("saves new settings, a new type, a disabled list and a new list disabled", () => {
    expect(keepsEnabled(enabled, "anilist_user", { username: "someone else" })).toBe(false);
    expect(keepsEnabled(enabled, "anilist_season", { username: "someone" })).toBe(false);
    expect(keepsEnabled({ ...enabled, enabled: false }, "anilist_user", { username: "someone" })).toBe(false);
    expect(keepsEnabled(undefined, "anilist_user", { username: "someone" })).toBe(false);
  });
});

describe("toggleChoice", () => {
  it("ticks and unticks one choice, keeping the field's own order", () => {
    const offered = ["CURRENT", "PLANNING", "COMPLETED"];
    expect(toggleChoice(offered, ["PLANNING"], "CURRENT")).toBe("CURRENT,PLANNING");
    expect(toggleChoice(offered, ["CURRENT", "PLANNING"], "CURRENT")).toBe("PLANNING");
    expect(toggleChoice(offered, ["COMPLETED", "CURRENT"], "PLANNING")).toBe("CURRENT,PLANNING,COMPLETED");
  });
});
