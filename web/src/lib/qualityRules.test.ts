import { describe, expect, it } from "vitest";

import { buildAudioRules, parseAudioRules, summarise } from "./qualityRules";

// The shipped "Lossless preferred", exactly as migration 0012 stores it.
const LOSSLESS_PREFERRED =
  '{"quality_profile":"Lossless preferred","compare":["lossless","sample_rate","bit_depth","bitrate_distance","audio_codec","score"],' +
  '"tiers":{"audio_codec":[["flac","alac"],["ogg","opus"],["aac"],["mp3"]]},' +
  '"targets":{"bitrate":[{"when":{"audio_codec":"mp3"},"min":"192kbps","ideal":"320kbps","max":"400kbps"},' +
  '{"when":{"audio_codec":"aac"},"min":"160kbps","ideal":"256kbps","max":"320kbps"}]}}';

describe("quality rules", () => {
  it("reads a shipped profile into the form", () => {
    const rules = parseAudioRules(LOSSLESS_PREFERRED);
    expect(rules.codecs).toEqual([["flac", "alac"], ["ogg", "opus"], ["aac"], ["mp3"]]);
    expect(rules.bitrates).toEqual([
      { codec: "mp3", min: "192", ideal: "320", max: "400" },
      { codec: "aac", min: "160", ideal: "256", max: "320" },
    ]);
    expect(summarise(LOSSLESS_PREFERRED)).toBe("flac/alac > ogg/opus > aac > mp3");
  });

  it("writes an unchanged form back as the same document", () => {
    const rebuilt = buildAudioRules(LOSSLESS_PREFERRED, "Lossless preferred", parseAudioRules(LOSSLESS_PREFERRED));
    expect(JSON.parse(rebuilt)).toEqual(JSON.parse(LOSSLESS_PREFERRED));
  });

  it("adds what the form sets before the score and keeps what it does not show", () => {
    const rules = parseAudioRules(LOSSLESS_PREFERRED);
    rules.channels = [["2.0"], ["5.1"]];
    rules.rejectCodecs = ["mp3"];
    rules.bitrates = [];
    const doc = JSON.parse(buildAudioRules(LOSSLESS_PREFERRED, "Mine", rules));
    expect(doc.quality_profile).toBe("Mine");
    expect(doc.compare).toEqual(["lossless", "sample_rate", "bit_depth", "audio_codec", "audio_channels", "score"]);
    expect(doc.tiers.audio_channels).toEqual([["2.0"], ["5.1"]]);
    expect(doc.require.reject_codecs).toEqual(["mp3"]);
    expect(doc.targets).toBeUndefined();
  });

  it("builds a new profile from nothing", () => {
    const doc = JSON.parse(
      buildAudioRules("", "AAC 256", {
        codecs: [["aac"]], containers: [["m4a"]], channels: [], rejectCodecs: [],
        bitrates: [{ codec: "aac", min: "224", ideal: "256", max: "320" }], sampleRate: null, bitDepth: null,
      }),
    );
    expect(doc.compare).toEqual(["audio_codec", "container", "bitrate_distance", "score"]);
    expect(doc.targets.bitrate).toEqual([{ when: { audio_codec: "aac" }, min: "224kbps", ideal: "256kbps", max: "320kbps" }]);
  });
});
