import {
  BookOpen,
  BookMarked,
  Disc3,
  Film,
  Flame,
  Gamepad2,
  Headphones,
  Library,
  Mic,
  Newspaper,
  Tv,
  SquarePlay,
  type LucideIcon,
} from "lucide-react";

// One icon per media type (settings.MediaTypes). Every library used to draw
// lucide's Library, a shelf of books, so the music library looked like a book
// library -- reported 2026-09-13.
const ICONS: Record<string, LucideIcon> = {
  music: Disc3,
  tv: Tv,
  movie: Film,
  audiobook: Headphones,
  book: BookOpen,
  adult: Flame,
  comic: BookMarked,
  rom: Gamepad2,
  podcast: Mic,
  news: Newspaper,
  youtube: SquarePlay,
};

/** The icon for a library of this media type, falling back to the generic
 *  shelf only for a type this map has not been taught yet. */
export function mediaTypeIcon(mediaType: string): LucideIcon {
  return ICONS[mediaType] ?? Library;
}
