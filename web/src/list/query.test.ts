import { describe, expect, test } from "vitest";

import { EMPTY, cycleSort, fromSearch, key, toSearch, withFilter } from "./query";

const FILTER_KEYS = ["state", "pool"];

describe("URL round-trip", () => {
  test("a view survives being linked and reopened", () => {
    // Losing sort and filters on reload is one of the *Arr irritations this is
    // meant to answer (Appendix B, complaint 2).
    const query = { filters: { state: "FAILED" }, sort: "created_at", direction: "desc" as const };
    expect(fromSearch(toSearch(query), FILTER_KEYS)).toEqual(query);
  });

  test("an untouched view has a clean URL", () => {
    expect(toSearch(EMPTY)).toBe("");
  });

  test("the default direction is left out, so identical views produce identical links", () => {
    expect(toSearch({ ...EMPTY, sort: "id" })).toBe("?sort=id");
  });

  test("unknown parameters are ignored rather than rejected", () => {
    // A URL is user input and arrives with tracking parameters, stale keys and
    // typos; none of that should be an error.
    const parsed = fromSearch("?state=FAILED&utm_source=mail&nonsense=1", FILTER_KEYS);
    expect(parsed.filters).toEqual({ state: "FAILED" });
  });

  test("an empty filter value is absent, not an empty string", () => {
    expect(fromSearch("?state=", FILTER_KEYS).filters).toEqual({});
  });
});

describe("sorting", () => {
  test("cycles ascending, descending, then off", () => {
    const first = cycleSort(EMPTY, "name");
    expect(first).toMatchObject({ sort: "name", direction: "asc" });

    const second = cycleSort(first, "name");
    expect(second).toMatchObject({ sort: "name", direction: "desc" });

    const third = cycleSort(second, "name");
    expect(third.sort).toBeNull();
  });

  test("a different column starts ascending", () => {
    const sorted = { ...EMPTY, sort: "name", direction: "desc" as const };
    expect(cycleSort(sorted, "size")).toMatchObject({ sort: "size", direction: "asc" });
  });
});

describe("cache identity", () => {
  test("filter insertion order does not change the key", () => {
    // Two ways of building the same filter must not fetch twice and then
    // disagree about what the list contains.
    const one = withFilter(withFilter(EMPTY, "state", "FAILED"), "pool", "import");
    const other = withFilter(withFilter(EMPTY, "pool", "import"), "state", "FAILED");
    expect(key(one)).toBe(key(other));
  });

  test("a different filter is a different key", () => {
    expect(key(withFilter(EMPTY, "state", "FAILED"))).not.toBe(
      key(withFilter(EMPTY, "state", "RUNNING")),
    );
  });

  test("clearing a filter returns to the original key", () => {
    const filtered = withFilter(EMPTY, "state", "FAILED");
    expect(key(withFilter(filtered, "state", ""))).toBe(key(EMPTY));
  });
});
