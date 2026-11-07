import { useState } from "react";

import { API_PREFIX } from "@/api/client";
import { useShowThumbnails } from "@/lib/interfacePreferences";

/** One preview picture of a video file, scaled to the height of the row it sits in; nothing at all when the file has
 *  none or the person turned thumbnails off. The single frame belongs in lists, the 3x3 sheet beside duplicates. */
export function FileThumb({ mediaObjectId, kind, wide = false, explain = false, className = "" }: { mediaObjectId: number; kind: "frame" | "sheet"; wide?: boolean; explain?: boolean; className?: string }) {
  const [shown] = useShowThumbnails();
  const [missing, setMissing] = useState(false);
  if (!shown) return null;
  // A picture a file lacks is only explained where it was asked for, and only the once.
  if (missing) return explain ? <p className="w-40 shrink-0 self-start text-[11px] text-ink-muted">No preview picture yet. Previews are made when a video is scanned with "Make preview pictures" on in Settings, Web UI.</p> : null;
  // The picture is laid over a box as high as the row and a share of the window wide, so it never sets the row's height.
  return (
    <div className={`relative self-stretch shrink-0 ${kind === "sheet" ? "w-[clamp(7rem,20vw,16rem)]" : wide ? "w-[clamp(8rem,16vw,16rem)]" : "w-[clamp(4rem,9vw,9rem)]"} ${className}`}>
      <img
        src={`${API_PREFIX}/files/${mediaObjectId}/preview/${kind}`}
        alt=""
        loading="lazy"
        onError={() => setMissing(true)}
        className="absolute inset-0 size-full rounded object-contain object-left"
      />
    </div>
  );
}
