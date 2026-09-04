import { useState } from "react";

import { API_PREFIX } from "@/api/client";

type Size = "thumb" | "small" | "full";

interface CoverArtProps {
  workId: number;
  title: string;
  size?: Size;
  className?: string;
}

// A work with no cover recorded yet answers 404 (section 21.6) -- onError
// swaps to a title-initial placeholder rather than the browser's broken-image
// icon. Reset per workId/size so a virtualised row reused for a different work
// does not keep showing a stale failure.
export function CoverArt({ workId, title, size = "small", className }: CoverArtProps) {
  const [failed, setFailed] = useState(false);
  const key = `${workId}-${size}`;

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
      src={`${API_PREFIX}/works/${workId}/artwork?size=${size}`}
      alt={title}
      loading="lazy"
      className={className}
      onError={() => setFailed(true)}
    />
  );
}
