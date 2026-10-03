import { describe, expect, it } from "vitest";

import { parseEpisodes } from "./IdentifyTVFile";

describe("parseEpisodes", () => {
  it("reads single numbers, lists and ranges", () => {
    expect(parseEpisodes("3")).toEqual([3]);
    expect(parseEpisodes("2, 3")).toEqual([2, 3]);
    expect(parseEpisodes("4-6")).toEqual([4, 5, 6]);
  });
  it("refuses what is not a list of episode numbers", () => {
    expect(parseEpisodes("two")).toBeNull();
    expect(parseEpisodes("6-4")).toBeNull();
    expect(parseEpisodes("1-500")).toBeNull();
  });
});
