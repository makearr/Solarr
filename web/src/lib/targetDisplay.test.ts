import { describe, expect, it } from "vitest";

import { targetSpecs, targetTitle } from "./targetDisplay";

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
