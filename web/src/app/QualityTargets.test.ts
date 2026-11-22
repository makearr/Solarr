import { describe, expect, it } from "vitest";

import { parsePageSize } from "./QualityTargets";

describe("parsePageSize", () => {
  it("stores the longer edge as long in either order", () => {
    expect(parsePageSize("3056 x 1988")).toEqual({ long: 3056, short: 1988 });
    expect(parsePageSize("1988x3056")).toEqual({ long: 3056, short: 1988 });
    expect(parsePageSize("4000×3000")).toEqual({ long: 4000, short: 3000 });
  });
  it("refuses what is not two numbers", () => {
    expect(parsePageSize("4000")).toBeUndefined();
    expect(parsePageSize("big x small")).toBeUndefined();
  });
});
