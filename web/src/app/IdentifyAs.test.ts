import { describe, expect, it } from "vitest";

import { parseParts, queryFromPath } from "./IdentifyAs";

describe("queryFromPath", () => {
  it("takes the artist and album folders, without the year", () => {
    expect(queryFromPath("Heka/Detisisu Binip (1991)/01 - Sifa.flac")).toBe("Heka Detisisu Binip");
  });

  it("keeps the two folders nearest the file, passing over a disc or season folder", () => {
    expect(queryFromPath("Music/Heka/Kuselobi [2004]/CD1/01.flac")).toBe("Heka Kuselobi");
    expect(queryFromPath("Marble Stations (2020)/Season 01/s01e02.mkv")).toBe("Marble Stations");
  });

  it("reads a folder's own path, ending in a slash, as its folders", () => {
    expect(queryFromPath("Heka/Kuselobi [2004]/")).toBe("Heka Kuselobi");
  });

  it("is empty for a file at the root", () => {
    expect(queryFromPath("01.flac")).toBe("");
  });
});

describe("parseParts", () => {
  it("reads single numbers, lists and ranges", () => {
    expect(parseParts("3")).toEqual([3]);
    expect(parseParts("2, 3")).toEqual([2, 3]);
    expect(parseParts("4-6")).toEqual([4, 5, 6]);
  });
  it("refuses what is not a list of numbers", () => {
    expect(parseParts("two")).toBeNull();
    expect(parseParts("6-4")).toBeNull();
    expect(parseParts("1-500")).toBeNull();
  });
});
