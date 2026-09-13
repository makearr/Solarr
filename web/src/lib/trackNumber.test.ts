import { describe, expect, it } from "vitest";

import { discCountOf, formatTrackNumber, parseTrackPosition } from "./trackNumber";

describe("parseTrackPosition", () => {
  it("reads the module's own media_ref convention", () => {
    expect(parseTrackPosition("disc1:track3", "00010003")).toEqual({ disc: 1, track: 3 });
    expect(parseTrackPosition("disc2:track11", null)).toEqual({ disc: 2, track: 11 });
  });

  it("falls back to the zero-padded sort key when media_ref is absent", () => {
    expect(parseTrackPosition(null, "00010003")).toEqual({ disc: 1, track: 3 });
    expect(parseTrackPosition(undefined, "00020012")).toEqual({ disc: 2, track: 12 });
  });

  it("answers null rather than guessing at anything else", () => {
    expect(parseTrackPosition(null, null)).toBeNull();
    expect(parseTrackPosition("", "")).toBeNull();
    expect(parseTrackPosition("track3", "3")).toBeNull();
  });
});

describe("formatTrackNumber", () => {
  it("prints the track alone on a single-disc release", () => {
    expect(formatTrackNumber("disc1:track3", "00010003", 1)).toBe("03");
    expect(formatTrackNumber("disc1:track12", "00010012", 1)).toBe("12");
  });

  it("qualifies with the disc once a release spans more than one", () => {
    expect(formatTrackNumber("disc2:track3", "00020003", 3)).toBe("2-03");
  });

  it("never leaks the raw sort key it was given", () => {
    expect(formatTrackNumber(null, "00010003", 1)).toBe("03");
    expect(formatTrackNumber(null, null, 1)).toBe("");
  });
});

describe("discCountOf", () => {
  it("counts the distinct discs a tracklist spans", () => {
    expect(discCountOf([{ media_ref: "disc1:track1" }, { media_ref: "disc1:track2" }])).toBe(1);
    expect(discCountOf([{ media_ref: "disc1:track1" }, { media_ref: "disc2:track1" }])).toBe(2);
  });

  it("never reports fewer than one disc", () => {
    expect(discCountOf([])).toBe(1);
    expect(discCountOf([{ media_ref: null, sort_key: null }])).toBe(1);
  });
});
