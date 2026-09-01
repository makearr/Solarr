import { describe, expect, test } from "vitest";

import {
  ALL,
  NOTHING,
  count,
  isEmpty,
  isSelected,
  selectRange,
  toRequest,
  toggle,
  type Selection,
} from "./selection";

describe("selecting by row", () => {
  test("toggling from nothing selects one", () => {
    const selection = toggle(NOTHING, 7);
    expect(selection).toEqual({ mode: "include", ids: new Set([7]) });
    expect(isSelected(selection, 7)).toBe(true);
    expect(isSelected(selection, 8)).toBe(false);
  });

  test("toggling the last selected row returns to nothing, not an empty set", () => {
    // Callers ask "is anything selected"; an empty include set answering "yes,
    // zero things" makes every one of them wrong.
    const selection = toggle(toggle(NOTHING, 7), 7);
    expect(selection).toEqual(NOTHING);
    expect(isEmpty(selection)).toBe(true);
  });
});

describe("selecting everything that matches", () => {
  test("all selects rows that were never loaded", () => {
    // The whole point of section 21.7: 231 matches, 12 rows fetched, and the
    // 231st is selected without the client having ever seen it.
    expect(isSelected(ALL, 999_999)).toBe(true);
  });

  test("deselecting from all records an exception, not 230 identifiers", () => {
    const selection = toggle(ALL, 5);
    expect(selection).toEqual({ mode: "all", excluded: new Set([5]) });
    expect(isSelected(selection, 5)).toBe(false);
    expect(isSelected(selection, 6)).toBe(true);
  });

  test("re-selecting an excluded row removes the exception", () => {
    expect(toggle(toggle(ALL, 5), 5)).toEqual(ALL);
  });

  test("the count subtracts exceptions from the server's total", () => {
    const selection = toggle(toggle(ALL, 1), 2);
    expect(count(selection, 231)).toBe(229);
  });

  test("the count is unknown until the server says how many match", () => {
    // Answering with the number of loaded rows would show "12 selected" to
    // someone who asked for all 231.
    expect(count(ALL, null)).toBeNull();
  });
});

describe("range selection", () => {
  const loaded = [10, 20, 30, 40, 50];

  test("selects the span between two loaded rows, in either direction", () => {
    const forwards = selectRange(NOTHING, loaded, 20, 40);
    const backwards = selectRange(NOTHING, loaded, 40, 20);
    expect(forwards).toEqual({ mode: "include", ids: new Set([20, 30, 40]) });
    expect(backwards).toEqual(forwards);
  });

  test("a range within an all-selection clears exceptions instead of adding ids", () => {
    const withHoles: Selection = { mode: "all", excluded: new Set([20, 30, 99]) };
    expect(selectRange(withHoles, loaded, 20, 30)).toEqual({
      mode: "all",
      excluded: new Set([99]),
    });
  });

  test("a range over rows that were never loaded changes nothing", () => {
    // The client only knows the order of what it has fetched. Guessing the rest
    // would select rows the user never saw.
    expect(selectRange(NOTHING, loaded, 20, 12345)).toEqual(NOTHING);
  });
});

describe("what goes to the server", () => {
  const filters = { state: "FAILED" };

  test("nothing selected sends nothing", () => {
    expect(toRequest(NOTHING, filters)).toBeNull();
  });

  test("a few rows are sent as ids", () => {
    expect(toRequest(toggle(toggle(NOTHING, 3), 1), filters)).toEqual({
      kind: "ids",
      ids: [1, 3],
    });
  });

  test("all-matching is sent as the query, never as materialised ids", () => {
    // Sending ids would mean fetching 231 of them first, and would act on a
    // stale set if anything changed between the click and the request.
    expect(toRequest(toggle(ALL, 5), filters)).toEqual({
      kind: "query",
      filters: { state: "FAILED" },
      excluded: [5],
    });
  });
});
