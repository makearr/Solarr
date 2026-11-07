import type { components } from "@/api/schema";
import type { ChipTone } from "@/ui/Chip";

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

export type FileChip = { text: string; tone?: ChipTone; title?: string };

const FIT_TONE = { ok: "ok", near: "caution", off: "danger" } as const;
const FIT_SAYS = { ok: "On the target.", near: "A little off the target.", off: "Far off the target." } as const;
type FitTag = "resolution" | "video_codec" | "audio_codec" | "audio_language" | "subtitle_language";

/** The hover text of a toned chip: how it stands and, for each difference, what was wanted and what the file has. */
function fitTitle(file: PartFile, tag: FitTag, fit: components["schemas"]["FitLevel"]): string {
  const notes = (file.target_fit?.notes ?? []).filter((note) => note.tag === tag);
  return [FIT_SAYS[fit], ...notes.map((note) => `${note.dimension}: wanted ${note.want || "none"}, the file has ${note.have || "none"}`)].join("\n");
}

function toned(file: PartFile, tag: FitTag, text: string | undefined): FileChip | undefined {
  const fit = file.target_fit?.[tag];
  if (text === undefined) return undefined;
  return fit ? { text, tone: FIT_TONE[fit], title: fitTitle(file, tag, fit) } : { text };
}

/** What the probe measured of a part's file, as short chips toned by how near each is to the library's target. */
export function fileTagChips(file: PartFile): FileChip[] {
  const known = (codes: readonly string[] | undefined) => (codes ?? []).filter((code) => code !== "und");
  const audio = file.audio_codec ? `${file.audio_codec.toUpperCase()}${file.audio_channels ? ` ${file.audio_channels}` : ""}` : undefined;
  const plain = (text: string | undefined): FileChip | undefined => (text === undefined ? undefined : { text });
  const hasSubtitles = file.subtitles && file.subtitles.length > 0;
  const subtitles = hasSubtitles ? (known(file.subtitles).length > 0 ? `subs: ${known(file.subtitles).join(", ")}` : "subs") : file.target_fit?.subtitle_language ? "subs: none" : undefined;
  const chips = file.video_codec
    ? [
        toned(file, "resolution", file.resolution),
        toned(file, "video_codec", file.video_codec.toUpperCase()),
        plain(file.hdr ? file.hdr.toUpperCase() : undefined),
        toned(file, "audio_codec", audio),
        toned(file, "audio_language", known(file.audio_languages).length > 0 ? `audio: ${known(file.audio_languages).join(", ")}` : undefined),
        toned(file, "subtitle_language", subtitles),
      ]
    : [toned(file, "audio_codec", file.extension?.toUpperCase() ?? audio), plain(file.lossless ? "lossless" : file.bitrate_kbps > 0 ? `${file.bitrate_kbps} kbps` : undefined)];
  return [...chips, plain(file.size_bytes ? formatSize(file.size_bytes) : undefined)].filter((chip): chip is FileChip => chip !== undefined && chip.text !== "");
}

/** The same chips as plain text. */
export const fileChips = (file: PartFile): string[] => fileTagChips(file).map((chip) => chip.text);

export type PartState = "present" | "missing" | "upcoming" | "unmonitored";

/** The number scene releases carry for an episode (TheXEM), as "S02E05"; undefined where it is the episode's own. */
export function sceneNumber(part: Pick<Part, "media_ref" | "alt_numbers">): string | undefined {
  const scene = part.alt_numbers?.scene;
  const match = scene && scene !== part.media_ref ? /^s(\d+):e(\d+)$/.exec(scene) : null;
  return match ? `S${match[1]}E${match[2]}` : undefined;
}

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
