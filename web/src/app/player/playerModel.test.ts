import { describe, expect, it } from "vitest";

import { firstRoute, formatClock, isPlayableExtension, routeAfterFailure, transcodeUrl } from "./playerModel";

describe("playerModel", () => {
  it("offers the player for media types only", () => {
    expect(isPlayableExtension("FLAC")).toBe(true);
    expect(isPlayableExtension(".mkv")).toBe(true);
    expect(isPlayableExtension("jpg")).toBe(false);
    expect(isPlayableExtension(undefined)).toBe(false);
  });

  it("starts converted only when the file is unlikely to play and a conversion exists", () => {
    expect(firstRoute({ direct_play_likely: true, transcode_available: true })).toBe("direct");
    expect(firstRoute({ direct_play_likely: false, transcode_available: true })).toBe("transcode");
    expect(firstRoute({ direct_play_likely: false, transcode_available: false })).toBe("direct");
  });

  it("falls back to a conversion once, never back", () => {
    expect(routeAfterFailure("direct", { transcode_available: true })).toBe("transcode");
    expect(routeAfterFailure("direct", { transcode_available: false })).toBeNull();
    expect(routeAfterFailure("transcode", { transcode_available: true })).toBeNull();
  });

  it("formats clocks", () => {
    expect(formatClock(65)).toBe("1:05");
    expect(formatClock(3725)).toBe("1:02:05");
    expect(formatClock(Number.NaN)).toBe("0:00");
  });

  it("asks a conversion to begin at a whole second", () => {
    expect(transcodeUrl(7, 90.9)).toBe("/api/v1/files/7/transcode?start=90");
  });
});
