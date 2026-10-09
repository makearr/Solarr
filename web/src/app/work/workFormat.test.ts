import { expect, test } from "vitest";

import type { components } from "@/api/schema";

import { externalLinks, fileChips, formatSize, partState } from "./workFormat";

type Part = components["schemas"]["WorkPart"];

const part = (over: Partial<Part>): Part => ({ id: 1, media_ref: "s01:e01", sort_key: "1", monitored: true, external_ids: [], ...over });

test("a part is on disk, missing once it is out, not out yet before, or not wanted", () => {
  const now = Date.parse("2026-10-09T12:00:00Z");
  const file = { media_object_id: 1, bitrate_kbps: 0, lossless: false };
  expect([
    partState(part({ file }), now),
    partState(part({ available_at: "2026-10-01T20:00:00Z" }), now),
    partState(part({ available_at: "2026-10-20T20:00:00Z" }), now),
    partState(part({}), now),
    partState(part({ monitored: false, date: "2020-01-01" }), now),
  ]).toEqual(["present", "missing", "upcoming", "upcoming", "unmonitored"]);
});

test("a video's chips name its picture and sound; an audio file's its format", () => {
  expect(
    fileChips({
      media_object_id: 1, bitrate_kbps: 0, lossless: false, video_codec: "hevc", resolution: "1080p", hdr: "hdr10",
      audio_codec: "eac3", audio_channels: "5.1", audio_languages: ["en", "und"], subtitles: ["und"], size_bytes: 1_200_000_000,
    }),
  ).toEqual(["1080p", "HEVC", "HDR10", "EAC3 5.1", "audio: en", "subs", "1.2 GB"]);
  expect(fileChips({ media_object_id: 2, bitrate_kbps: 320, lossless: false, extension: "mp3", size_bytes: 9_000_000 })).toEqual(["MP3", "320 kbps", "9.0 MB"]);
  expect(formatSize(950)).toBe("950 B");
});

test("a work links each provider's page once, and leaves out ids nobody browses", () => {
  expect(
    externalLinks([
      { provider: "tvdb", external_id: "81189" },
      { provider: "tvdb", external_id: "99" },
      { provider: "local:folder", external_id: "x" },
    ]),
  ).toEqual([{ label: "TheTVDB", href: "https://thetvdb.com/dereferrer/series/81189" }]);
});
