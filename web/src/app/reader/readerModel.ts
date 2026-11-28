import { API_PREFIX } from "@/api/client";

export type ReaderKind = "comic" | "epub" | "pdf";

const COMIC_EXTENSIONS = ["cbz", "cbr", "cb7", "cbt"];

/** What opens a file in the reader, by its extension; null for a file the reader cannot show. */
export function readerKindOf(extension: string | undefined): ReaderKind | null {
  const lower = (extension ?? "").replace(/^\./, "").toLowerCase();
  if (COMIC_EXTENSIONS.includes(lower)) return "comic";
  if (lower === "epub") return "epub";
  if (lower === "pdf") return "pdf";
  return null;
}

export function pageUrl(mediaObjectId: number, index: number): string {
  return `${API_PREFIX}/files/${mediaObjectId}/pages/${index}`;
}

export function fileUrl(mediaObjectId: number): string {
  return `${API_PREFIX}/files/${mediaObjectId}/stream`;
}

/** The pages on screen when the reader stands at a page: one, or two side by side with the cover (page 0) alone, as a
 *  printed book opens. */
export function spreadAt(position: number, count: number, double: boolean): number[] {
  if (count <= 0) return [];
  const page = Math.min(Math.max(position, 0), count - 1);
  if (!double || page === 0) return [page];
  const first = page % 2 === 1 ? page : page - 1;
  return first + 1 < count ? [first, first + 1] : [first];
}

/** The page the next (forward) or previous (backward) turn lands on; the same page at either end. */
export function turn(position: number, count: number, double: boolean, forward: boolean): number {
  const shown = spreadAt(position, count, double);
  if (shown.length === 0) return 0;
  if (forward) return Math.min((shown[shown.length - 1] ?? 0) + 1, count - 1);
  const first = shown[0] ?? 0;
  return first <= 0 ? 0 : (spreadAt(first - 1, count, double)[0] ?? 0);
}

/** Which way an arrow key or a click on the left edge turns: left is forward when reading right to left. */
export function leftTurnsForward(rightToLeft: boolean): boolean {
  return rightToLeft;
}

/** How far through a comic a page is, 0 to 1. */
export function progressOf(position: number, count: number): number {
  return count <= 1 ? 0 : Math.min(Math.max(position / (count - 1), 0), 1);
}
