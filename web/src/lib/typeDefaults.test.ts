import { expect, test } from "vitest";

import { inheritedLabel, moveItem } from "./typeDefaults";

test("an item moves one place and an edge stays put", () => {
  expect(moveItem(["a", "b", "c"], 1, -1)).toEqual(["b", "a", "c"]);
  expect(moveItem(["a", "b", "c"], 2, 1)).toEqual(["a", "b", "c"]);
});

test("the default entry names the inherited choice, else says none", () => {
  expect(inheritedLabel(true, "Lossless", "None")).toBe("Type default: Lossless");
  expect(inheritedLabel(false, "Lossless", "None")).toBe("None");
  expect(inheritedLabel(true, undefined, "None")).toBe("None");
});
