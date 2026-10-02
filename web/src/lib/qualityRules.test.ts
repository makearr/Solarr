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
        codecs: [["aac"]], extensions: [], containers: [["mp4"]], channels: [], rejectCodecs: [],
        bitrates: [{ codec: "aac", min: "224", ideal: "256", max: "320" }], sampleRate: null, bitDepth: null,
        languages: { audio: [], lyrics: [], subtitle: [] },
      }),
    );
    expect(doc.compare).toEqual(["audio_codec", "container", "bitrate_distance", "score"]);
    expect(doc.targets.bitrate).toEqual([{ when: { audio_codec: "aac" }, min: "224kbps", ideal: "256kbps", max: "320kbps" }]);
  });

  it("keeps extensions and containers apart, reading an older profile's m4a as the MP4 container", () => {
    const older = '{"quality_profile":"x","compare":["container","score"],"tiers":{"container":[["m4a"],["flac"],["mp3"]]}}';
    const rules = parseAudioRules(older);
    expect(rules.containers).toEqual([["mp4"], ["flac"], ["mp3"]]);
    rules.extensions = [["m4a"], ["mp4"]];
    const rebuilt = JSON.parse(buildAudioRules(older, "x", rules));
    expect(rebuilt.tiers.extension).toEqual([["m4a"], ["mp4"]]);
    expect(rebuilt.compare).toEqual(["container", "extension", "score"]);
  });

  it("keeps the profile's own scoring rules and writes language preferences as rules of their own", () => {
    const own = '{"quality_profile":"x","compare":["score"],"scoring":[{"name":"Scene group","score":1,"specs":[{"field":"release_group","matches":"X"}]}]}';
    const rules = parseAudioRules(own);
    expect(rules.languages).toEqual({ audio: [], lyrics: [], subtitle: [] });
    rules.languages.lyrics = ["de", "en"];
    const doc = JSON.parse(buildAudioRules(own, "x", rules));
    expect(doc.scoring).toEqual([
      { name: "Scene group", score: 1, specs: [{ field: "release_group", matches: "X" }] },
      { name: "Preferred lyrics language: de", score: 2, specs: [{ field: "lyrics_languages", has: "de", required: true }] },
      { name: "Preferred lyrics language: en", score: 2, specs: [{ field: "lyrics_languages", has: "en", required: true }] },
    ]);
    expect(parseAudioRules(JSON.stringify(doc)).languages.lyrics).toEqual(["de", "en"]);
    rules.languages.lyrics = [];
    expect(JSON.parse(buildAudioRules(JSON.stringify(doc), "x", rules)).scoring).toEqual(JSON.parse(own).scoring);
  });
});
