import { expect, test } from "vitest";

import { targetFromProfile } from "./QualityProfiles";

test("one exact quality starts at the first profile's own bitrate target", () => {
  const rules = JSON.stringify({ targets: { bitrate: [{ when: { audio_codec: "aac" }, min: "144kbps", ideal: "160kbps", max: "176kbps" }] } });
  expect(targetFromProfile(rules)).toEqual({ audio_codec: "aac", container: "m4a", bitrate_kbps: 160, tolerance_pct: 10 });
});

test("a profile without a bitrate target, or none at all, starts at lossless", () => {
  const lossless = { audio_codec: "flac", container: "flac", bitrate_kbps: undefined, tolerance_pct: 10 };
  expect(targetFromProfile(JSON.stringify({ tiers: { audio_codec: [["flac"]] } }))).toEqual(lossless);
  expect(targetFromProfile(undefined)).toEqual(lossless);
});
