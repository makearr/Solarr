import { describe, expect, it } from "vitest";

import { familyOf, preferredTarget, targetSpecs, targetTitle } from "./targetDisplay";

describe("targetTitle and targetSpecs", () => {
  it("shows the short name big and the specification small", () => {
    const target = { name: "1080p, 30 fps, H.265, MP4", label: "Space conscious" };
    expect(targetTitle(target)).toBe("Space conscious");
    expect(targetSpecs(target, "summary")).toBe("1080p, 30 fps, H.265, MP4");
  });

  it("falls back to the name, with the summary beneath", () => {
    expect(targetTitle({ name: "Mine" })).toBe("Mine");
    expect(targetSpecs({ name: "Mine" }, ".mkv · 720p")).toBe(".mkv · 720p");
  });
});

describe("preferredTarget", () => {
  const targets = [
    { id: 1, name: "a", label: "Space conscious", media_type: "video" },
    { id: 2, name: "b", label: "Space conscious", media_type: "music" },
    { id: 3, name: "c", label: "4K archive", media_type: "video" },
  ] as never[];

  it("is the family's Space conscious target, shared by every media type in it", () => {
    expect(familyOf("adult")).toBe("video");
    expect(preferredTarget(targets, "movie")?.id).toBe(1);
    expect(preferredTarget(targets, "tv")?.id).toBe(1);
    expect(preferredTarget(targets, "music")?.id).toBe(2);
    expect(preferredTarget(targets, "rom")).toBeUndefined();
  });
});
