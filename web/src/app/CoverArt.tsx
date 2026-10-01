import { useState } from "react";

import { API_PREFIX } from "@/api/client";

import { useArtworkVersion } from "./artworkVersion";

type Size = "thumb" | "small" | "full";
type Subject = "works" | "people";

interface CoverArtProps {
  /** Which /{subject}/{id}/artwork endpoint to read from -- "works" (an
   *  album's own cover, itself falling back server-side to its primary
   *  artist's image when it has none) or "people" (an artist's own Wikidata
   *  photo directly, 2026-09-11's own fallback chain). Defaults to "works",
   *  every caller before people existed. */
  subject?: Subject;
  workId: number;
  title: string;
  size?: Size;
  className?: string;
}

// A subject with no image recorded yet answers 404 (section 21.6) -- onError
// swaps to a title-initial placeholder rather than the browser's broken-image
// icon. Reset per subject/workId/size so a virtualised row reused for a
// different subject does not keep showing a stale failure.
export function CoverArt({ subject = "works", workId, title, size = "small", className }: CoverArtProps) {
  const [failed, setFailed] = useState(false);
  const version = useArtworkVersion(subject, workId);
  const key = `${subject}-${workId}-${size}-${version}`;

  if (failed) {
    return (
      <span
        key={key}
        className={`flex items-center justify-center border border-border-subtle bg-surface text-ink-muted ${className ?? ""}`}
        aria-label={title}
      >
        {title.trim().charAt(0).toUpperCase() || "?"}
      </span>
    );
  }

  return (
    <img
      key={key}
      src={`${API_PREFIX}/${subject}/${workId}/artwork?size=${size}${version > 0 ? `&v=${version}` : ""}`}
      alt={title}
      loading="lazy"
      className={className}
      onError={() => setFailed(true)}
    />
  );
}
