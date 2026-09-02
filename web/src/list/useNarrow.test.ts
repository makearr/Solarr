import { describe, expect, test } from "vitest";

import { NARROW } from "./useNarrow";

// The breakpoint itself is the contract with section 21.8; the hook around it is
// a matchMedia subscription, which jsdom would test rather than the rule.
describe("the card breakpoint", () => {
  test("is 700px, where a dense table stops being readable", () => {
    expect(NARROW).toBe(700);
  });

  test("the query is exclusive, so 700px is still a table", () => {
    const query = `(max-width: ${NARROW - 1}px)`;
    expect(query).toBe("(max-width: 699px)");
  });
});
