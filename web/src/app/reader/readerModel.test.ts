import { describe, expect, it } from "vitest";

import { leftTurnsForward, progressOf, readerKindOf, spreadAt, turn } from "./readerModel";

describe("readerKindOf", () => {
  it("opens comic archives, EPUB and PDF by extension, in any case, and nothing else", () => {
    expect(readerKindOf("cbz")).toBe("comic");
    expect(readerKindOf(".CBR")).toBe("comic");
    expect(readerKindOf("epub")).toBe("epub");
    expect(readerKindOf("pdf")).toBe("pdf");
    expect(readerKindOf("mkv")).toBeNull();
    expect(readerKindOf(undefined)).toBeNull();
  });
});

describe("spreadAt", () => {
  it("shows one page, or the cover alone then pairs", () => {
    expect(spreadAt(3, 10, false)).toEqual([3]);
    expect(spreadAt(0, 10, true)).toEqual([0]);
    expect(spreadAt(1, 10, true)).toEqual([1, 2]);
    expect(spreadAt(2, 10, true)).toEqual([1, 2]);
    expect(spreadAt(9, 10, true)).toEqual([9]);
    expect(spreadAt(99, 10, false)).toEqual([9]);
  });
});

describe("turn", () => {
  it("steps one page, or one pair, and stops at the ends", () => {
    expect(turn(3, 10, false, true)).toBe(4);
    expect(turn(3, 10, false, false)).toBe(2);
    expect(turn(0, 10, false, false)).toBe(0);
    expect(turn(9, 10, false, true)).toBe(9);
    expect(turn(0, 10, true, true)).toBe(1);
    expect(turn(1, 10, true, true)).toBe(3);
    expect(turn(3, 10, true, false)).toBe(1);
    expect(turn(1, 10, true, false)).toBe(0);
    expect(turn(0, 10, true, false)).toBe(0);
  });
});

describe("direction and progress", () => {
  it("turns forward on the left only when reading right to left", () => {
    expect(leftTurnsForward(true)).toBe(true);
    expect(leftTurnsForward(false)).toBe(false);
  });
  it("is 0 on the first page and 1 on the last", () => {
    expect(progressOf(0, 10)).toBe(0);
    expect(progressOf(9, 10)).toBe(1);
    expect(progressOf(0, 1)).toBe(0);
  });
});
