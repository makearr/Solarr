import { describe, expect, test } from "vitest";

import { groupLayout } from "./useLibraryList";

const byArtist = (album: { artist: string }) => album.artist;

describe("groupLayout", () => {
  test("puts a header above each group and wraps its items at the row width", () => {
    const albums = [
      { artist: "Heka" },
      { artist: "Heka" },
      { artist: "Heka" },
      { artist: "P!po" },
    ];

    expect(groupLayout(albums, 2, byArtist)).toEqual([
      { kind: "group", label: "Heka" },
      { kind: "items", start: 0, end: 2 },
      { kind: "items", start: 2, end: 3 },
      { kind: "group", label: "P!po" },
      { kind: "items", start: 3, end: 4 },
    ]);
  });

  // A group never borrows the next group's items to fill a short row.
  test("ends a short row at the group boundary", () => {
    const albums = [{ artist: "Heka" }, { artist: "P!po" }, { artist: "P!po" }];

    expect(groupLayout(albums, 4, byArtist)).toEqual([
      { kind: "group", label: "Heka" },
      { kind: "items", start: 0, end: 1 },
      { kind: "group", label: "P!po" },
      { kind: "items", start: 1, end: 3 },
    ]);
  });

  test("lays out nothing for no items", () => {
    expect(groupLayout([], 3, byArtist)).toEqual([]);
  });
});
