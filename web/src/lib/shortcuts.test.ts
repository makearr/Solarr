import { expect, test } from "vitest";

import { isTyping, neighbour } from "./shortcuts";

test("single-letter shortcuts stay out of the way while typing", () => {
  expect(isTyping(document.createElement("input"))).toBe(true);
  expect(isTyping(document.createElement("select"))).toBe(true);
  expect(isTyping(document.createElement("div"))).toBe(false);
  expect(isTyping(null)).toBe(false);
});

test("j and k move one row from the focused one, starting at either end when none is focused", () => {
  const rows = [document.createElement("div"), document.createElement("div"), document.createElement("div")];
  expect(neighbour(rows, null, 1)).toBe(rows[0]);
  expect(neighbour(rows, null, -1)).toBe(rows[2]);
  expect(neighbour(rows, rows[0]!, 1)).toBe(rows[1]);
  expect(neighbour(rows, rows[1]!, -1)).toBe(rows[0]);
  expect(neighbour(rows, rows[2]!, 1)).toBeUndefined();
});
