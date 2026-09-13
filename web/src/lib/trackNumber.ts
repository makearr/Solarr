/** The disc and track a part sits at. media_ref is the module's own
 *  "disc1:track3" convention (internal/media/music/enrich.go); sort_key is
 *  the zero-padded "%04d%04d" ordering key beside it, read only when
 *  media_ref is missing or shaped differently. */
export interface TrackPosition {
  disc: number;
  track: number;
}

const MEDIA_REF = /^disc(\d+):track(\d+)$/;
const SORT_KEY = /^(\d{4})(\d{4})$/;

export function parseTrackPosition(mediaRef?: string | null, sortKey?: string | null): TrackPosition | null {
  const fromRef = mediaRef?.match(MEDIA_REF);
  if (fromRef) return { disc: Number(fromRef[1]), track: Number(fromRef[2]) };

  const fromSort = sortKey?.match(SORT_KEY);
  if (fromSort) return { disc: Number(fromSort[1]), track: Number(fromSort[2]) };

  return null;
}

/** What the tracklist prints in its number column: "03", or "2-03" once a
 *  release has more than one disc. The raw sort_key used to be printed here
 *  verbatim, so a track read as "00010003 Beautiful" (reported 2026-09-13). */
export function formatTrackNumber(
  mediaRef?: string | null,
  sortKey?: string | null,
  discCount = 1,
): string {
  const position = parseTrackPosition(mediaRef, sortKey);
  if (!position) return "";
  const track = String(position.track).padStart(2, "0");
  return discCount > 1 ? `${position.disc}-${track}` : track;
}

/** How many discs a set of parts spans, so a single-disc release does not
 *  carry a redundant "1-" on every row. */
export function discCountOf(parts: readonly { media_ref?: string | null; sort_key?: string | null }[]): number {
  const discs = new Set<number>();
  for (const part of parts) {
    const position = parseTrackPosition(part.media_ref, part.sort_key);
    if (position) discs.add(position.disc);
  }
  return Math.max(1, discs.size);
}
