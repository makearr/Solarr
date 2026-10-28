import type { components } from "@/api/schema";

type Part = components["schemas"]["WorkPart"];
type PartFile = components["schemas"]["PartFile"];
type ExternalID = components["schemas"]["ExternalID"];

/** "3:07" from milliseconds. */
export function formatDuration(ms: number): string {
  const totalSeconds = Math.round(ms / 1000);
  return `${Math.floor(totalSeconds / 60)}:${String(totalSeconds % 60).padStart(2, "0")}`;
}

/** "55 min" from milliseconds, for a work's running time. */
export function formatMinutes(ms: number): string {
  return `${Math.round(ms / 60_000)} min`;
}

/** "1.2 GB" from bytes. */
export function formatSize(bytes: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1000 && unit < units.length - 1) {
    value /= 1000;
    unit++;
  }
  return `${value >= 10 || unit === 0 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`;
}

/** "3 hours ago" or "in 5 hours", from Unix seconds against now (milliseconds). */
export function relativeTime(at: number, now = Date.now()): string {
  const minutes = Math.round((at * 1000 - now) / 60_000);
  const format = new Intl.RelativeTimeFormat("en", { numeric: "auto" });
  if (Math.abs(minutes) < 60) return format.format(minutes, "minute");
  if (Math.abs(minutes) < 48 * 60) return format.format(Math.round(minutes / 60), "hour");
  return format.format(Math.round(minutes / 1440), "day");
}

/** What the probe measured of a part's file, as short chips: a video's picture and sound, an audio file's format. */
export function fileChips(file: PartFile): string[] {
  const known = (codes: readonly string[] | undefined) => (codes ?? []).filter((code) => code !== "und");
  const audio = file.audio_codec ? `${file.audio_codec.toUpperCase()}${file.audio_channels ? ` ${file.audio_channels}` : ""}` : undefined;
  const chips = file.video_codec
    ? [
        file.resolution,
        file.video_codec.toUpperCase(),
        file.hdr ? file.hdr.toUpperCase() : undefined,
        audio,
        known(file.audio_languages).length > 0 ? `audio: ${known(file.audio_languages).join(", ")}` : undefined,
        file.subtitles && file.subtitles.length > 0 ? (known(file.subtitles).length > 0 ? `subs: ${known(file.subtitles).join(", ")}` : "subs") : undefined,
      ]
    : [file.extension?.toUpperCase() ?? audio, file.lossless ? "lossless" : file.bitrate_kbps > 0 ? `${file.bitrate_kbps} kbps` : undefined];
  return [...chips, file.size_bytes ? formatSize(file.size_bytes) : undefined].filter((chip): chip is string => Boolean(chip));
}

export type PartState = "present" | "missing" | "upcoming" | "unmonitored";

/** Where a part stands: on disk, wanted and missing, not out yet, or not wanted. */
export function partState(part: Part, now = Date.now()): PartState {
  if (part.file) return "present";
  if (!part.monitored) return "unmonitored";
  const out = part.available_at ? Date.parse(part.available_at) : part.date ? Date.parse(part.date) : undefined;
  if (out === undefined || Number.isNaN(out) || out > now) return "upcoming";
  return "missing";
}

// A provider's page for an id, where it has one people browse.
const LINKS: Record<string, { label: string; url: (id: string) => string }> = {
  tvdb: { label: "TheTVDB", url: (id) => `https://thetvdb.com/dereferrer/series/${id}` },
  tvmaze: { label: "TVmaze", url: (id) => `https://www.tvmaze.com/shows/${id}` },
  imdb: { label: "IMDb", url: (id) => `https://www.imdb.com/title/${id}/` },
  tmdb: { label: "TMDB", url: (id) => `https://www.themoviedb.org/tv/${id}` },
  "tmdb:movie": { label: "TMDB", url: (id) => `https://www.themoviedb.org/movie/${id}` },
  "musicbrainz:release-group": { label: "MusicBrainz", url: (id) => `https://musicbrainz.org/release-group/${id}` },
  openlibrary: { label: "Open Library", url: (id) => `https://openlibrary.org/works/${id}` },
  "openlibrary:audiobook": { label: "Open Library", url: (id) => `https://openlibrary.org/works/${id}` },
  googlebooks: { label: "Google Books", url: (id) => `https://books.google.com/books?id=${id}` },
  "googlebooks:audiobook": { label: "Google Books", url: (id) => `https://books.google.com/books?id=${id}` },
  goodreads: { label: "Goodreads", url: (id) => `https://www.goodreads.com/work/show/${id}` },
  "goodreads:audiobook": { label: "Goodreads", url: (id) => `https://www.goodreads.com/work/show/${id}` },
  hardcover: { label: "Hardcover", url: (id) => `https://hardcover.app/search?q=${id}` },
  "hardcover:audiobook": { label: "Hardcover", url: (id) => `https://hardcover.app/search?q=${id}` },
  itunes: { label: "iTunes", url: (id) => `https://books.apple.com/audiobook/id${id}` },
  "itunes:audiobook": { label: "iTunes", url: (id) => `https://books.apple.com/audiobook/id${id}` },
  fantlab: { label: "FantLab", url: (id) => `https://fantlab.ru/work${id}` },
  "fantlab:audiobook": { label: "FantLab", url: (id) => `https://fantlab.ru/work${id}` },
  audible: { label: "Audible", url: (id) => `https://www.audible.com/pd/${id}` },
};

/** The provider pages of a work's ids, once each. */
export function externalLinks(ids: readonly ExternalID[]): { label: string; href: string }[] {
  const seen = new Set<string>();
  const links: { label: string; href: string }[] = [];
  for (const id of ids) {
    const link = LINKS[id.provider];
    if (link && !seen.has(id.provider)) {
      seen.add(id.provider);
      links.push({ label: link.label, href: link.url(id.external_id) });
    }
  }
  return links;
}

/** A work known only by its own tags or folder carries nothing but local ids (D6). */
export function isUnverified(ids: readonly ExternalID[]): boolean {
  return ids.length > 0 && ids.every((id) => id.provider.startsWith("local:"));
}
