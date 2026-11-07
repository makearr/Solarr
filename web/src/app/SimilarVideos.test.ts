import { describe, expect, it } from "vitest";

import { bitsApart } from "./SimilarVideos";

describe("bitsApart", () => {
  it("counts the differing bits of two 64-bit hashes", () => {
    expect(bitsApart("0000000000000000", "0000000000000000")).toBe(0);
    expect(bitsApart("ffffffffffffffff", "0000000000000000")).toBe(64);
    expect(bitsApart("00000000000000f0", "000000000000000f")).toBe(8);
  });
});
