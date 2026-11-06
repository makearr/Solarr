import { describe, expect, it } from "vitest";

import { applyProfile, recommendedTarget } from "./targetProfile";

const recommended = { id: 7, name: "1080p, 30 fps", media_type: "tv", audio_codec: "aac", container: "mp4", tolerance_pct: 0, resolution: "1080p", crf: 28, label: "Space conscious", builtin: true };
const anime = { id: 3, name: "Anime", media_types: ["tv"], recommended_target: "1080p, 30 fps", crf: 24, encoder_tune: "animation", deinterlace: false, description: "Flat colours." };

describe("applyProfile", () => {
  it("finds the recommended target of the media type", () => {
    expect(recommendedTarget(anime, "tv", [recommended])?.id).toBe(7);
    expect(recommendedTarget(anime, "movie", [recommended])).toBeUndefined();
  });

  it("starts from the recommended target, lays the profile's suggestions on top and links it", () => {
    const made = applyProfile({ name: "Mine", media_type: "tv", audio_codec: "", container: "", tolerance_pct: 0 }, anime, [recommended]);
    expect(made).toMatchObject({ name: "Mine", container: "mp4", resolution: "1080p", crf: 24, encoder_tune: "animation", profile_id: 3, description: "Flat colours." });
    expect(made.id).toBeUndefined();
    expect(made.label).toBeUndefined();
  });
});
