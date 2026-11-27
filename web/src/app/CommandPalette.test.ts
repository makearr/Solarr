import { expect, test } from "vitest";

import { matches } from "./CommandPalette";

test("every word typed must appear in the label or its hint, in any order and case", () => {
  const entry = { label: "Settings: Download clients", hint: "Connections" };
  expect(matches(entry, "download")).toBe(true);
  expect(matches(entry, "CLIENTS connections")).toBe(true);
  expect(matches(entry, "indexers")).toBe(false);
  expect(matches(entry, "")).toBe(true);
});
