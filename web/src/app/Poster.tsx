import { Link } from "@tanstack/react-router";
import type { LinkProps } from "@tanstack/react-router";
import { UserRound } from "lucide-react";

import { AvailabilityBar, type AlbumStatus } from "@/lib/albumStatus";

import { CoverArt } from "./CoverArt";

/** One tile on the poster wall: a square cover with its name beneath, the
 *  shape Lidarr's artist and album views use. Circular for a person, square
 *  for a release -- the same distinction the old row thumbnails drew.
 *  Reads `small` (400px): a tile is 168-300px, so `full` (1000px) was
 *  downloading roughly six times the bytes a wall of them can show. */
export function Poster({
  subject,
  imageId,
  title,
  subtitle,
  badge,
  topLeft,
  hoverActions,
  showTitle = true,
  link,
  round = false,
  status,
  presentCount,
  requiredCount,
}: {
  subject: "works" | "people";
  imageId: number | null;
  title: string;
  subtitle?: string;
  badge?: React.ReactNode;
  // Radarr's poster: the monitored bookmark top left, actions over the cover on hover (2026-09-30).
  topLeft?: React.ReactNode;
  hoverActions?: React.ReactNode;
  showTitle?: boolean;
  link: LinkProps | null;
  round?: boolean;
  // status absent means this tile has nothing to report, and carries no bar
  // whatever the preference says.
  status?: AlbumStatus;
  presentCount?: number;
  requiredCount?: number;
}) {
  const shape = round ? "rounded-full" : "rounded-lg";
  const body = (
    <>
      <span className="relative block">
        {imageId != null ? (
          <CoverArt
            subject={subject}
            workId={imageId}
            title={title}
            size="small"
            className={`aspect-square w-full bg-surface object-cover text-2xl ${shape}
                        border border-border-subtle group-hover:border-accent`}
          />
        ) : (
          <span
            className={`flex aspect-square w-full items-center justify-center border border-border-subtle
                        bg-surface text-2xl text-ink-muted ${shape}`}
          >
            {title.trim().charAt(0).toUpperCase() || "?"}
          </span>
        )}
        {badge && <span className="absolute right-1.5 top-1.5">{badge}</span>}
        {topLeft && <span className="absolute left-1.5 top-1.5">{topLeft}</span>}
        {hoverActions && (
          <span className="absolute inset-x-1.5 bottom-1.5 flex justify-end opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100">
            {hoverActions}
          </span>
        )}
      </span>
      {status && (
        <AvailabilityBar
          status={status}
          present={presentCount}
          required={requiredCount}
          className="mt-1.5"
        />
      )}
      {showTitle && <span className="mt-2 block truncate text-sm font-medium group-hover:text-accent">{title}</span>}
      {showTitle && subtitle && <span className="block truncate text-xs text-ink-muted">{subtitle}</span>}
    </>
  );

  if (!link) return <div className="group block">{body}</div>;
  return (
    <Link
      {...link}
      className="group block focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
    >
      {body}
    </Link>
  );
}

/** Marks an album shown by its artist's image, having no cover of its own (asked 2026-09-30). */
export function ArtistCoverMark() {
  return (
    <span
      title="No album cover yet: this is the artist's image"
      aria-label="Artist image, no album cover"
      className="flex size-6 items-center justify-center rounded-full border border-border-subtle bg-canvas/85 text-ink-muted shadow-sm"
    >
      <UserRound aria-hidden className="size-3.5" />
    </span>
  );
}
