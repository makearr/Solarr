import { describe, expect, it } from "vitest";

import { queryFromPath } from "./ManualAlbumSearch";

describe("queryFromPath", () => {
  it("takes the artist and album folders, without the year", () => {
    expect(queryFromPath("Heka/Detisisu Binip (1991)/01 - Sifa.flac")).toBe("Heka Detisisu Binip");
  });

  it("keeps the two folders nearest the file, passing over a disc folder", () => {
    expect(queryFromPath("Music/Heka/Kuselobi [2004]/CD1/01.flac")).toBe("Heka Kuselobi");
  });

  it("is empty for a file at the root", () => {
    expect(queryFromPath("01.flac")).toBe("");
  });
});
