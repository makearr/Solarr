import { API_PREFIX } from "@/api/client";

/** Where the player sits: a bar under the page, a small window in the lower right corner, or the whole screen. */
export type PlayerMode = "bar" | "mini" | "full";

export const PLAYER_MODES: readonly PlayerMode[] = ["bar", "mini", "full"];

/** One thing to play: a library file and the words to show for it. */
export type PlayerItem = {
  mediaObjectId: number;
  title: string;
  subtitle?: string;
  /** The catalogue's length, for a file the probe has not measured yet. */
  durationMs?: number;
};

const MEDIA_EXTENSIONS = new Set([
  "mp3", "flac", "ogg", "oga", "opus", "m4a", "aac", "wav", "wma", "ape", "wv", "alac", "aiff", "aif", "dsf", "mka",
  "mkv", "mp4", "m4v", "avi", "mov", "webm", "wmv", "ts", "m2ts", "mpg", "mpeg", "flv", "ogv",
]);

/** Whether a file's type is one the player can be offered for; the server still has the last word. */
export function isPlayableExtension(extension: string | undefined): boolean {
  return extension !== undefined && MEDIA_EXTENSIONS.has(extension.toLowerCase().replace(/^\./, ""));
}

export function streamUrl(mediaObjectId: number): string {
  return `${API_PREFIX}/files/${mediaObjectId}/stream`;
}

/** The converted stream from `startSecond`; a transcode has no length of its own, so a seek asks again from the new place. */
export function transcodeUrl(mediaObjectId: number, startSecond: number): string {
  return `${API_PREFIX}/files/${mediaObjectId}/transcode?start=${Math.max(0, Math.floor(startSecond))}`;
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
