import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Film } from "lucide-react";
import { useState } from "react";

import { API_PREFIX, api, ok } from "@/api/client";
import { Dialog } from "@/ui/Dialog";
import { useShowThumbnails } from "@/lib/interfacePreferences";

/** One preview picture of a video file, scaled to the height of the row it sits in; nothing at all when the person turned
 *  thumbnails off. The single frame belongs in a work's file rows, the 3x3 sheet beside duplicates. */
export function FileThumb({ mediaObjectId, kind, wide = false, placeholder = false, className = "" }: { mediaObjectId: number; kind: "frame" | "sheet"; wide?: boolean; placeholder?: boolean; className?: string }) {
  const [shown] = useShowThumbnails();
  const [missing, setMissing] = useState(false);
  const [enlarged, setEnlarged] = useState(false);
  if (!shown) return null;
  if (missing) return placeholder ? <PreviewPlaceholder wide={wide} className={className} /> : null;
  const src = `${API_PREFIX}/files/${mediaObjectId}/preview/${kind}`;
  // Every picture gets one fixed shape, so a column of them lines up whatever the video's shape or the row's height.
  const box = kind === "sheet" ? "h-24 w-40 self-start" : `self-start aspect-video ${wide ? "w-[clamp(8rem,16vw,16rem)]" : "w-[clamp(4rem,9vw,9rem)]"}`;
  return (
    <>
      <button
        type="button"
        aria-label="Show the picture at full size"
        onClick={(event) => {
          event.preventDefault();
          event.stopPropagation();
          setEnlarged(true);
        }}
        className={`relative shrink-0 cursor-zoom-in ${box} ${className}`}
      >
        <img src={src} alt="" loading="lazy" onError={() => setMissing(true)} className={`absolute inset-0 size-full rounded bg-surface object-contain object-center`} />
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
function usePreviewPlaceholders(): boolean {
  const [shown] = useShowThumbnails();
  const general = useQuery({ queryKey: ["general-settings"], queryFn: () => ok(api.GET("/settings/general")) });
  return shown && general.data?.video_previews === true;
}

const VIDEO_MEDIA_TYPES: ReadonlySet<string> = new Set(["tv", "movie", "adult"]);

/** Whether a media type's files are videos, so its file rows carry a preview frame. */
export function isVideoType(mediaType: string | undefined): boolean {
  return mediaType !== undefined && VIDEO_MEDIA_TYPES.has(mediaType);
}

/** Stands where a video's preview picture will be, with the way to the settings that make it. */
function PreviewPlaceholder({ wide = false, className = "" }: { wide?: boolean; className?: string }) {
  const navigate = useNavigate();
  const shown = usePreviewPlaceholders();
  if (!shown) return null;
  const size = `self-start aspect-video ${wide ? "w-[clamp(8rem,16vw,16rem)]" : "w-[clamp(4rem,9vw,9rem)]"}`;
  return (
    <div
      title="No preview picture yet. Open Settings, General to generate the missing previews."
      className={`flex shrink-0 flex-col items-center justify-center gap-0.5 rounded border border-dashed border-border-subtle text-ink-muted ${size} ${className}`}
    >
      <Film aria-hidden className="size-4" />
      <button
        type="button"
        onClick={(event) => {
          event.preventDefault();
          event.stopPropagation();
          void navigate({ to: "/settings/general" });
        }}
        className="text-[10px] text-accent hover:underline"
      >
        Settings
      </button>
    </div>
  );
}
