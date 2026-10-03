import { describe, expect, it } from "vitest";

import { buildAudioRules, buildVideoRules, parseAudioRules, parseVideoRules, summarise } from "./qualityRules";

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

// A shipped TV profile in the shape migration 0032 stores it.
const WEB_1080P =
  '{"quality_profile":"WEB 1080p","require":{"resolution":{"min":"720p","max":"1080p"},"reject_codecs":["xvid","divx"]},' +
  '"compare":["resolution","source","video_codec","audio_channels","audio_codec","score"],' +
  '"tiers":{"resolution":[["1080p","1080i"],["720p"],["576p","480p","360p"],["2160p"]],"source":[["webdl"],["webrip"],["bluray","remux"],["hdtv"]],' +
  '"video_codec":[["h264"],["h265","av1"]],"audio_channels":[["5.1"],["2.0"]],"audio_codec":[["eac3","ac3"],["aac"]]}}';

describe("video quality rules", () => {
  it("reads a shipped TV profile into the form and writes it back unchanged", () => {
    const rules = parseVideoRules(WEB_1080P);
    expect(rules.resolutions[0]).toEqual(["1080p", "1080i"]);
    expect(rules.minResolution).toBe("720p");
    expect(rules.maxResolution).toBe("1080p");
    expect(rules.rejectCodecs).toEqual(["xvid", "divx"]);
    expect(summarise(WEB_1080P)).toBe("1080p/1080i > 720p > 576p/480p/360p > 2160p");
    expect(JSON.parse(buildVideoRules(WEB_1080P, "WEB 1080p", rules))).toEqual(JSON.parse(WEB_1080P));
  });

  it("adds a preferred group as a scoring rule and removes it again", () => {
    const rules = parseVideoRules(WEB_1080P);
    rules.preferredGroups = ["MeGusta"];
    const added = JSON.parse(buildVideoRules(WEB_1080P, "WEB 1080p", rules));
    expect(added.scoring).toEqual([
      { name: "Preferred release group: MeGusta", score: 5, specs: [{ field: "release_group", eq: "MeGusta", required: true }] },
    ]);
    expect(parseVideoRules(JSON.stringify(added)).preferredGroups).toEqual(["MeGusta"]);
    rules.preferredGroups = [];
    expect(JSON.parse(buildVideoRules(JSON.stringify(added), "WEB 1080p", rules)).scoring).toBeUndefined();
  });

  it("sets and clears the resolution limit and the audio language gate, and ranks a new dimension before the score", () => {
    const rules = parseVideoRules(WEB_1080P);
    rules.minResolution = "";
    rules.maxResolution = "";
    rules.audioLanguages = ["ja", "en"];
    rules.hdr = [["hdr10"]];
    const built = JSON.parse(buildVideoRules(WEB_1080P, "WEB 1080p", rules));
    expect(built.require.resolution).toBeUndefined();
    expect(built.require.audio_language).toEqual({ any_of: ["ja", "en"] });
    expect(built.compare.at(-1)).toBe("score");
    expect(built.compare).toContain("hdr");
    expect(built.tiers.hdr).toEqual([["hdr10"]]);
  });

  it("keeps targets and rules the form does not show", () => {
    const withTarget = JSON.stringify({ ...JSON.parse(WEB_1080P), targets: { bitrate_distance: [{ ideal: "8Mbps" }] }, scoring: [{ name: "Own rule", score: 3, specs: [] }] });
    const rebuilt = JSON.parse(buildVideoRules(withTarget, "WEB 1080p", parseVideoRules(withTarget)));
    expect(rebuilt.targets).toEqual({ bitrate_distance: [{ ideal: "8Mbps" }] });
    expect(rebuilt.scoring).toEqual([{ name: "Own rule", score: 3, specs: [] }]);
  });
});
