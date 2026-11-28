import { describe, expect, it } from "vitest";

import { DEFAULT_FOOTER, normaliseDashboard, normaliseFooter } from "./interfacePreferences";

describe("normaliseFooter", () => {
  it("starts with the address and the clock hidden", () => {
    expect(DEFAULT_FOOTER).toEqual([
      { item: "events", shown: true }, { item: "work", shown: true }, { item: "queue", shown: true },
      { item: "ip", shown: false }, { item: "time", shown: false }, { item: "version", shown: true },
    ]);
  });

  it("keeps the stored order, drops unknown and repeated items, and appends missing ones", () => {
    const stored = [{ item: "version", shown: false }, { item: "gone", shown: true }, { item: "ip", shown: true }, { item: "version", shown: true }];
    expect(normaliseFooter(stored as never)).toEqual([
      { item: "version", shown: false }, { item: "ip", shown: true },
      { item: "events", shown: true }, { item: "work", shown: true }, { item: "queue", shown: true }, { item: "time", shown: false },
    ]);
  });
});

it("keeps the dashboard's stored order and appends sections added since", () => {
  const stored = [{ widget: "recent", shown: false }, { widget: "gone", shown: true }, { widget: "recent", shown: true }, { widget: "setup", shown: true }] as never;
  expect(normaliseDashboard(stored).map((entry) => `${entry.widget}:${entry.shown}`)).toEqual([
    "recent:false", "setup:true", "attention:true", "scanning:true", "discover:true", "downloads:true", "libraries:true",
  ]);
});
