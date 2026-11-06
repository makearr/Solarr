import type { components } from "@/api/schema";

type QualityTarget = components["schemas"]["QualityTarget"];

/** The families of media types that share their target qualities, in the order they are shown. */
export const TARGET_FAMILIES: readonly { key: string; label: string; holds: string }[] = [
  { key: "video", label: "Video", holds: "movies, series, adult" },
  { key: "music", label: "Music", holds: "music" },
  { key: "spoken", label: "Spoken", holds: "audiobooks, podcasts" },
  { key: "book", label: "Books", holds: "books, comics" },
];

/** The family a media type's targets belong to, as the server groups them. */
export function familyOf(mediaType: string): string {
  if (["tv", "movie", "adult", "youtube", "news", "video"].includes(mediaType)) return "video";
  if (["audiobook", "podcast", "spoken"].includes(mediaType)) return "spoken";
  if (["book", "comic"].includes(mediaType)) return "book";
  return mediaType;
}

/** The preferred ("Space conscious") target of a media type's family, which a new library starts with; undefined when it has none. */
export function preferredTarget(targets: readonly QualityTarget[], mediaType: string): QualityTarget | undefined {
  const family = familyOf(mediaType);
  return targets.find((target) => target.media_type === family && target.label === "Space conscious");
}

/** What a target is called in a list: its short name when it has one, else its name. */
export function targetTitle(target: Pick<QualityTarget, "name" | "label">): string {
  return target.label || target.name || "";
}

/** The specification shown small beneath the title: the full name when a short name stands in for it, else nothing extra. */
export function targetSpecs(target: Pick<QualityTarget, "name" | "label">, summary: string): string {
  return target.label ? (target.name ?? "") : summary;
}
