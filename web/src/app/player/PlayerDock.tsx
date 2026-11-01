import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Maximize2, PanelBottom, PictureInPicture2, Pause, Play, SkipBack, SkipForward, Volume2, VolumeX, X } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";
import { Button } from "@/ui/Button";

import { usePlayer } from "./PlayerContext";
import {
  firstRoute,
  formatClock,
  routeAfterFailure,
  streamUrl,
  transcodeUrl,
  type PlaybackRoute,
  type PlayerItem,
  type PlayerMode,
} from "./playerModel";

type FilePlayback = components["schemas"]["FilePlayback"];

/** The player's surface, kept in the page frame so it survives navigation: a bar under the page, a window in the
 *  lower right corner, or the whole screen. */
export function PlayerDock() {
  const player = usePlayer();
  if (!player.current) return null;
  // Keyed so a new file starts with a fresh route, position and error.
  return <Dock key={player.current.mediaObjectId} item={player.current} />;
}

const FRAME: Record<PlayerMode, string> = {
  bar: "shrink-0 flex items-center gap-3 border-t border-border-subtle bg-surface px-3 py-2",
  mini: "fixed bottom-4 right-4 z-40 flex w-80 max-w-[calc(100vw-2rem)] flex-col overflow-hidden rounded-lg border border-border-subtle bg-surface shadow-xl",
  full: "fixed inset-0 z-50 flex flex-col bg-black text-white",
};

const VIDEO: Record<PlayerMode, string> = {
  bar: "h-12 w-20 shrink-0 rounded bg-black object-contain",
  mini: "aspect-video w-full bg-black object-contain",
  full: "min-h-0 w-full flex-1 bg-black object-contain",
};

function Dock({ item }: { item: PlayerItem }) {
  const player = usePlayer();
  const mode = player.mode;
  const media = useRef<HTMLVideoElement>(null);

  const info = useQuery({
    queryKey: ["playback", item.mediaObjectId],
    queryFn: () => ok(api.GET("/files/{mediaObjectId}/playback", { params: { path: { mediaObjectId: item.mediaObjectId } } })),
    retry: false,
    staleTime: 60_000,
  });

  const [chosenRoute, setChosenRoute] = useState<PlaybackRoute | null>(null);
  const [offset, setOffset] = useState(0);
  const [clock, setClock] = useState(0);
  const [playing, setPlaying] = useState(true);
  const [muted, setMuted] = useState(false);
  const [failed, setFailed] = useState(false);

  const facts: FilePlayback | undefined = info.data;
  const route = chosenRoute ?? (facts ? firstRoute(facts) : null);
  const position = route === "transcode" ? offset + clock : clock;
  const length = lengthOf(facts, item, media.current);
  const isVideo = facts?.kind === "video";

  // Escape leaves the full screen for the bar rather than closing the player.
  useEffect(() => {
    if (mode !== "full") return;
    const onKey = (event: KeyboardEvent) => event.key === "Escape" && player.setMode("bar");
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [mode, player]);

  const seek = (second: number) => {
    if (route === "transcode") {
      setOffset(second);
      setClock(0);
    } else if (media.current) {
      media.current.currentTime = second;
    }
  };

  const failedRoute = () => {
    const other = facts && route ? routeAfterFailure(route, facts) : null;
    if (other) {
      setOffset(position);
      setClock(0);
      setChosenRoute(other);
    } else {
      setFailed(true);
      setPlaying(false);
    }
  };

  const src = route === null ? undefined : route === "direct" ? streamUrl(item.mediaObjectId) : transcodeUrl(item.mediaObjectId, offset);
  const unplayable = info.isError || failed;

  return (
    <section aria-label={t("player.label")} data-region="player" className={FRAME[mode]}>
      <video
        ref={media}
        key={`${route}-${offset}`}
        src={src}
        autoPlay
        muted={muted}
        playsInline
        className={isVideo ? VIDEO[mode] : "hidden"}
        onTimeUpdate={(event) => setClock(event.currentTarget.currentTime)}
        onPlay={() => setPlaying(true)}
        onPause={() => setPlaying(false)}
        onError={failedRoute}
        onEnded={player.hasNext ? player.next : undefined}
        onClick={() => togglePlay(media.current)}
      />
      <div className={mode === "bar" ? "flex min-w-0 flex-1 items-center gap-3" : `flex flex-col gap-2 p-3 ${mode === "full" ? "mt-auto" : ""}`}>
        <div className={mode === "bar" ? "min-w-0 basis-56" : "flex items-start gap-2"}>
          <div className="min-w-0 flex-1">
            <div className="truncate text-sm font-medium">{item.title}</div>
            <div className={`truncate text-xs ${mode === "full" ? "text-white/70" : "text-ink-muted"}`}>
              {unplayable ? t("player.failed") : (item.subtitle ?? "")}
              {!unplayable && route === "transcode" && ` · ${t("player.converted")}`}
            </div>
          </div>
          {mode !== "bar" && <Modes mode={mode} onClose={player.close} />}
        </div>
        <div className="flex items-center gap-1">
          <Button variant="icon" aria-label={t("player.previous")} disabled={!player.hasPrevious} onClick={player.previous}>
            <SkipBack aria-hidden className="size-4" />
          </Button>
          <Button variant="icon" aria-label={playing ? t("player.pause") : t("player.play")} onClick={() => togglePlay(media.current)}>
            {playing ? <Pause aria-hidden className="size-5" /> : <Play aria-hidden className="size-5" />}
          </Button>
          <Button variant="icon" aria-label={t("player.next")} disabled={!player.hasNext} onClick={player.next}>
            <SkipForward aria-hidden className="size-4" />
          </Button>
        </div>
        <div className="flex min-w-0 flex-1 items-center gap-2 text-xs tabular-nums">
          <span>{formatClock(position)}</span>
          <input
            type="range"
            aria-label={t("player.seek")}
            className="min-w-0 flex-1 accent-accent"
            min={0}
            max={length > 0 ? length : 1}
            step={1}
            value={length > 0 ? Math.min(position, length) : 0}
            disabled={length <= 0}
            onChange={(event) => seek(Number(event.target.value))}
          />
          <span>{length > 0 ? formatClock(length) : "--:--"}</span>
        </div>
        <Button variant="icon" aria-label={muted ? t("player.unmute") : t("player.mute")} onClick={() => setMuted(!muted)}>
          {muted ? <VolumeX aria-hidden className="size-4" /> : <Volume2 aria-hidden className="size-4" />}
        </Button>
      </div>
      {mode === "bar" && <Modes mode={mode} onClose={player.close} />}
    </section>
  );
}

function lengthOf(facts: FilePlayback | undefined, item: PlayerItem, element: HTMLVideoElement | null): number {
  if (facts?.duration_ms) return facts.duration_ms / 1000;
  if (item.durationMs) return item.durationMs / 1000;
  return element && Number.isFinite(element.duration) ? element.duration : 0;
}

function togglePlay(element: HTMLVideoElement | null) {
  if (!element) return;
  if (element.paused) void element.play().catch(() => undefined);
  else element.pause();
}

const MODE_ICONS = { bar: PanelBottom, mini: PictureInPicture2, full: Maximize2 } as const;

/** Where the player sits, and closing it. */
function Modes({ mode, onClose }: { mode: PlayerMode; onClose: () => void }) {
  const player = usePlayer();
  return (
    <div className="flex shrink-0 items-center gap-0.5" role="group" aria-label={t("player.placement")}>
      {(["bar", "mini", "full"] as const).map((choice) => {
        const Icon = MODE_ICONS[choice];
        return (
          <Button
            key={choice}
            variant="icon"
            aria-pressed={choice === mode}
            aria-label={t(`player.mode.${choice}`)}
            title={t(`player.mode.${choice}`)}
            className={choice === mode ? "text-accent" : ""}
            onClick={() => player.setMode(choice)}
          >
            <Icon aria-hidden className="size-4" />
          </Button>
        );
      })}
      <Button variant="icon" aria-label={t("player.close")} title={t("player.close")} onClick={onClose}>
        <X aria-hidden className="size-4" />
      </Button>
    </div>
  );
}
