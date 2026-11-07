import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Film } from "lucide-react";
import { useState } from "react";

import { API_PREFIX, api, ok } from "@/api/client";
import { Dialog } from "@/ui/Dialog";
import { useShowThumbnails } from "@/lib/interfacePreferences";

/** One preview picture of a video file, scaled to the height of the row it sits in; nothing at all when the file has
 *  none or the person turned thumbnails off. The single frame belongs in lists, the 3x3 sheet beside duplicates. */
export function FileThumb({ mediaObjectId, kind, wide = false, explain = false, className = "" }: { mediaObjectId: number; kind: "frame" | "sheet"; wide?: boolean; explain?: boolean; className?: string }) {
  const [shown] = useShowThumbnails();
  const [missing, setMissing] = useState(false);
  const [enlarged, setEnlarged] = useState(false);
  if (!shown) return null;
  // A picture a file lacks is only explained where it was asked for, and only the once.
  if (missing) return explain ? <p className="w-40 shrink-0 self-start text-[11px] text-ink-muted">No preview picture yet. Previews are made when a video is scanned with "Make preview pictures" on in Settings, Web UI.</p> : null;
  const src = `${API_PREFIX}/files/${mediaObjectId}/preview/${kind}`;
  // Sheets share one fixed box so a column of them lines up whatever the video's shape; a frame fills the row's height.
  const box = kind === "sheet" ? "h-24 w-40 self-start" : `self-stretch ${wide ? "w-[clamp(8rem,16vw,16rem)]" : "w-[clamp(4rem,9vw,9rem)]"}`;
  return (
    <>
      <button
        type="button"
        aria-label="Show the picture at full size"
        onClick={(event) => {
          event.stopPropagation();
          setEnlarged(true);
        }}
        className={`relative shrink-0 cursor-zoom-in ${box} ${className}`}
      >
        <img src={src} alt="" loading="lazy" onError={() => setMissing(true)} className={`absolute inset-0 size-full rounded object-contain ${kind === "sheet" ? "object-center" : "object-left"}`} />
      </button>
      {enlarged && (
        <Dialog title="Preview" width="wide" onClose={() => setEnlarged(false)}>
          <img src={src} alt="" className="mx-auto max-h-[70vh] max-w-full rounded object-contain" />
        </Dialog>
      )}
    </>
  );
}

/** Whether a video with no preview picture yet shows a placeholder: previews on in Settings, General, thumbnails on in Web UI. */
export function usePreviewPlaceholders(): boolean {
  const [shown] = useShowThumbnails();
  const general = useQuery({ queryKey: ["general-settings"], queryFn: () => ok(api.GET("/settings/general")) });
  return shown && general.data?.video_previews === true;
}

const VIDEO_MEDIA_TYPES: ReadonlySet<string> = new Set(["tv", "movie", "adult"]);

/** Whether a work is a video type that holds files, so a missing preview picture is worth a placeholder. */
export function isPreviewable(work: { media_type: string; size_bytes?: number; present_count?: number }): boolean {
  return VIDEO_MEDIA_TYPES.has(work.media_type) && ((work.size_bytes ?? 0) > 0 || (work.present_count ?? 0) > 0);
}

/** Stands where a video's preview picture will be, with the way to the settings that make it. */
export function PreviewPlaceholder({ wide = false }: { wide?: boolean }) {
  if (!usePreviewPlaceholders()) return null;
  return (
    <div
      title="No preview picture yet. Open Settings, General to generate the missing previews."
      className={`flex shrink-0 flex-col items-center justify-center gap-0.5 self-stretch rounded border border-dashed border-border-subtle text-ink-muted ${wide ? "w-[clamp(8rem,16vw,16rem)]" : "w-[clamp(4rem,9vw,9rem)]"}`}
    >
      <Film aria-hidden className="size-4" />
      <Link to="/settings/general" onClick={(event) => event.stopPropagation()} className="text-[10px] text-accent hover:underline">
        Settings
      </Link>
    </div>
  );
}
