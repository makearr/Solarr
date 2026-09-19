import type { components } from "@/api/schema";

type PersonWork = components["schemas"]["PersonWork"];
export type AlbumStatus = PersonWork["status"];

// One colour vocabulary for every place an album is listed (2026-09-19):
// red incomplete, purple downloading, yellow not released yet, green
// complete. The server decides which one; nothing here re-derives it.
const FILL: Record<AlbumStatus, string> = {
  complete: "bg-ok",
  incomplete: "bg-danger",
  downloading: "bg-busy",
  unreleased: "bg-warn",
};

const TEXT: Record<AlbumStatus, string> = {
  complete: "text-ok",
  incomplete: "text-danger",
  downloading: "text-busy",
  unreleased: "text-warn",
};

const LABEL: Record<AlbumStatus, string> = {
  complete: "Complete",
  incomplete: "Missing",
  downloading: "Downloading",
  unreleased: "Not released yet",
};

export function albumStatusLabel(status: AlbumStatus): string {
  return LABEL[status];
}

export function albumStatusText(status: AlbumStatus): string {
  return TEXT[status];
}

/** The bar under a cover: how much of the album is here, in the status
 *  colour. A fully present album still shows a full bar rather than
 *  nothing, so a row of covers reads at a glance. */
export function AvailabilityBar({
  status,
  present,
  required,
  className = "",
}: {
  status: AlbumStatus;
  present?: number;
  required?: number;
  className?: string;
}) {
  const total = required ?? 0;
  const have = present ?? 0;
  const fraction = total > 0 ? Math.min(1, have / total) : status === "complete" ? 1 : 0;
  const title =
    total > 0 ? `${have} of ${total} — ${LABEL[status]}` : LABEL[status];

  return (
    <div
      role="img"
      aria-label={title}
      title={title}
      className={`h-1 w-full overflow-hidden rounded-full bg-border-subtle ${className}`}
    >
      <div className={`h-full ${FILL[status]}`} style={{ width: `${fraction * 100}%` }} />
    </div>
  );
}

export function albumStatusFill(status: AlbumStatus): string {
  return FILL[status];
}
