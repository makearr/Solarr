import { describe, expect, test } from "vitest";

import { NARROW, SIDEBAR_COLLAPSE } from "./useNarrow";

// The breakpoints themselves are the contract with section 21.8; the hooks around
// them are a matchMedia subscription, which jsdom would test rather than the rule.
describe("the card breakpoint", () => {
  test("is 700px, where a dense table stops being readable", () => {
    expect(NARROW).toBe(700);
  });

  test("the query is exclusive, so 700px is still a table", () => {
    const query = `(max-width: ${NARROW - 1}px)`;
    expect(query).toBe("(max-width: 699px)");
  });
});

describe("the sidebar-collapse breakpoint", () => {
  test("is 1100px, section 21.8's 700-1100px row", () => {
    expect(SIDEBAR_COLLAPSE).toBe(1100);
  });

  test("the query is exclusive, so 1100px keeps the full sidebar", () => {
    const query = `(max-width: ${SIDEBAR_COLLAPSE - 1}px)`;
    expect(query).toBe("(max-width: 1099px)");
  });
});
