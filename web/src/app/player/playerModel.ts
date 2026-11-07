import { API_PREFIX } from "@/api/client";
import type { components } from "@/api/schema";

/** Where the player sits: a bar under the page, a small window in the lower right corner, a large window over the page
 *  (cinema), or the whole screen. */
export type PlayerMode = "bar" | "mini" | "cinema" | "full";

export const PLAYER_MODES: readonly PlayerMode[] = ["bar", "mini", "cinema", "full"];

/** One thing to play: a library file and the words to show for it. */
export type PlayerItem = {
  mediaObjectId: number;
  title: string;
  subtitle?: string;
  /** The catalogue's length, for a file the probe has not measured yet. */
  durationMs?: number;
  /** What the library knows of the file, shown as the same chips its work page shows. */
  file?: components["schemas"]["PartFile"];
};

const MEDIA_EXTENSIONS = new Set([
  "mp3", "flac", "ogg", "oga", "opus", "m4a", "aac", "wav", "wma", "ape", "wv", "alac", "aiff", "aif", "dsf", "mka",
  "mkv", "mp4", "m4v", "avi", "mov", "webm", "wmv", "ts", "m2ts", "mpg", "mpeg", "flv", "ogv",
]);

/** Whether a file's type is one the player can be offered for; the server still has the last word. */
export function isPlayableExtension(extension: string | undefined): boolean {
  return extension !== undefined && MEDIA_EXTENSIONS.has(extension.toLowerCase().replace(/^\./, ""));
}

/** The subtitle as WebVTT; a converted stream began at `startSecond`, so the cues move back by it. */
export function subtitleUrl(mediaObjectId: number, track: number, startSecond: number): string {
  return `${API_PREFIX}/files/${mediaObjectId}/subtitles/${track}?start=${Math.max(0, Math.floor(startSecond))}`;
}

/** "English" from a language code the track states, else the track's own title, else its number. */
export function subtitleLabel(track: { id: number; language?: string; title?: string }): string {
  let name = "";
  if (track.language) {
    try {
      name = new Intl.DisplayNames(["en"], { type: "language" }).of(track.language) ?? "";
    } catch {
      name = track.language;
    }
  }
  if (name === "" || name === track.language) name = track.language ?? "";
  return [name, track.title && track.title !== name ? track.title : ""].filter(Boolean).join(" · ") || `Subtitle ${track.id + 1}`;
}

export function streamUrl(mediaObjectId: number): string {
  return `${API_PREFIX}/files/${mediaObjectId}/stream`;
}

/** How much of the picture a viewer keeps: the file as it is, or scaled down with a lower bitrate. */
export type Quality = "original" | "1080p" | "720p" | "480p";

export const QUALITIES: readonly Quality[] = ["original", "1080p", "720p", "480p"];

/** The converted stream from `startSecond`; a transcode has no length of its own, so a seek asks again from the new place. */
export function transcodeUrl(mediaObjectId: number, startSecond: number, quality: Quality = "original"): string {
  return `${API_PREFIX}/files/${mediaObjectId}/transcode?start=${Math.max(0, Math.floor(startSecond))}&quality=${quality}`;
}

/** "6.2 Mbit/s" from kbit/s; "" when unknown. */
export function formatBitrate(kbps: number | undefined): string {
  if (!kbps || kbps <= 0) return "";
  return kbps >= 1000 ? `${(kbps / 1000).toFixed(1)} Mbit/s` : `${Math.round(kbps)} kbit/s`;
}

/** The original file in a line: picture size, codec and bitrate, whichever the probe measured. */
export function describeSource(facts: {
  width?: number;
  height?: number;
  video_codec?: string;
  overall_bitrate_kbps?: number;
  video_bitrate_kbps?: number;
}): string {
  const size = facts.width && facts.height ? `${facts.width}×${facts.height}` : "";
  const bitrate = formatBitrate(facts.overall_bitrate_kbps ?? facts.video_bitrate_kbps);
  return [size, facts.video_codec?.toUpperCase(), bitrate].filter(Boolean).join(" · ");
}

/** m:ss, or h:mm:ss from an hour. */
export function formatClock(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "0:00";
  const whole = Math.floor(seconds);
  const hours = Math.floor(whole / 3600);
  const minutes = Math.floor((whole % 3600) / 60);
  const rest = String(whole % 60).padStart(2, "0");
  return hours > 0 ? `${hours}:${String(minutes).padStart(2, "0")}:${rest}` : `${minutes}:${rest}`;
}

/** How the file reaches the browser: as it is, or converted by the server. */
export type PlaybackRoute = "direct" | "transcode";

/** The route to start with: the file as it is when a browser likely plays it, else the conversion if there is one. */
export function firstRoute(info: { direct_play_likely: boolean; transcode_available: boolean }): PlaybackRoute {
  return info.direct_play_likely || !info.transcode_available ? "direct" : "transcode";
}

/** Whether a failed route has another to try: only the direct one has, and only when a conversion exists. */
export function routeAfterFailure(
  route: PlaybackRoute,
  info: { transcode_available: boolean },
): PlaybackRoute | null {
  return route === "direct" && info.transcode_available ? "transcode" : null;
}
