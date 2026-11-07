import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Maximize2, PanelBottom, PictureInPicture2, Pause, Play, RectangleHorizontal, SkipBack, SkipForward, Volume2, VolumeX, X } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";
import { Button } from "@/ui/Button";
import { Chip } from "@/ui/Chip";

import { TargetChip } from "../work/TargetChip";
import { fileChips } from "../work/workFormat";

import { usePlayer } from "./PlayerContext";
import {
  QUALITIES,
  describeSource,
  firstRoute,
  formatClock,
  routeAfterFailure,
  streamUrl,
  subtitleLabel,
  subtitleUrl,
  transcodeUrl,
  type PlaybackRoute,
  type PlayerItem,
  type PlayerMode,
  type Quality,
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
  mini: "fixed z-40 flex flex-col overflow-hidden rounded-lg border border-border-subtle bg-surface shadow-xl",
  cinema: "fixed z-40 flex max-h-[92vh] flex-col overflow-hidden rounded-lg border border-border-subtle bg-surface shadow-2xl",
  full: "fixed inset-0 z-50 flex flex-col bg-black text-white",
};

const VIDEO: Record<PlayerMode, string> = {
  bar: "h-14 w-24 shrink-0 rounded bg-black object-contain",
  mini: "aspect-video w-full bg-black object-contain",
  cinema: "aspect-video max-h-[70vh] w-full bg-black object-contain",
  full: "min-h-0 w-full flex-1 bg-black object-contain",
};

function Dock({ item }: { item: PlayerItem }) {
  const player = usePlayer();
  const mode = player.mode;
  const media = useRef<HTMLVideoElement>(null);
  const content = useContentBox();

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
  const [volume, setVolume] = useState(storedVolume);
  const [failed, setFailed] = useState(false);
  const [quality, setQuality] = useState<Quality>("original");
  const [subtitle, setSubtitle] = useState<number | null>(null);
  // Where a direct file resumes after a switch back from a converted stream, which restarts it at zero.
  const resumeAt = useRef(0);

  const facts: FilePlayback | undefined = info.data;
  const isVideo = facts?.kind === "video";
  // A lower quality is always a conversion; the original goes the way the file likes.
  const route = quality !== "original" && isVideo ? "transcode" : (chosenRoute ?? (facts ? firstRoute(facts) : null));
  const position = route === "transcode" ? offset + clock : clock;
  const length = lengthOf(facts, item, media.current);

  // React sets `muted` only when the element is created, and the element is re-keyed on every source change.
  const chosenSource = sourceUrl(route, item.mediaObjectId, offset, quality);
  useEffect(() => {
    if (!media.current) return;
    media.current.muted = muted;
    media.current.volume = volume;
  }, [muted, volume, chosenSource]);
  const changeVolume = (next: number) => {
    setVolume(next);
    setMuted(next === 0);
    try {
      localStorage.setItem(VOLUME_KEY, String(next));
    } catch {
      // storage unavailable: the volume lasts for this player only
    }
  };

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

  const chooseQuality = (next: Quality) => {
    resumeAt.current = position;
    setOffset(position);
    setClock(0);
    setFailed(false);
    setQuality(next);
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

  const tracks = facts?.subtitles ?? [];
  const chosenTrack = tracks.find((track) => track.id === subtitle);
  const src = chosenSource;
  const unplayable = info.isError || failed;

  return (
    <section aria-label={t("player.label")} data-region="player" style={floatingStyle(mode, content)} className={`${mode === "bar" ? "relative" : ""} ${FRAME[mode]}`}>
      <video
        ref={media}
        key={`${route}-${offset}-${quality}`}
        src={src}
        autoPlay
        muted={muted}
        playsInline
        className={isVideo ? VIDEO[mode] : "hidden"}
        onTimeUpdate={(event) => setClock(event.currentTarget.currentTime)}
        onPlay={() => setPlaying(true)}
        onPause={() => setPlaying(false)}
        onLoadedMetadata={(event) => {
          if (route === "direct" && resumeAt.current > 0) event.currentTarget.currentTime = resumeAt.current;
          resumeAt.current = 0;
        }}
        onError={failedRoute}
        onEnded={player.hasNext ? player.next : undefined}
        onClick={() => togglePlay(media.current)}
      >
        {isVideo && chosenTrack && (
          <track key={chosenTrack.id} kind="subtitles" default src={subtitleUrl(item.mediaObjectId, chosenTrack.id, route === "transcode" ? offset : 0)} srcLang={chosenTrack.language || undefined} label={subtitleLabel(chosenTrack)} />
        )}
      </video>
      {mode !== "bar" && (
        <div className="absolute right-2 top-2 z-10 rounded-md bg-black/60 p-0.5 text-white">
          <Modes mode={mode} onClose={player.close} />
        </div>
      )}
      <div className={mode === "bar" ? "flex min-w-0 flex-1 items-center gap-3" : `flex flex-col gap-2 p-3 ${mode === "full" ? "mt-auto" : ""}`}>
        <div className={mode === "bar" ? "min-w-0 basis-56" : "flex items-start gap-2"}>
          <div className="min-w-0 flex-1">
            <div className="truncate text-sm font-medium">{item.title}</div>
            <div className={`truncate text-xs ${mode === "full" ? "text-white/70" : "text-ink-muted"}`}>
              {[unplayable ? t("player.failed") : item.subtitle, facts ? describeSource(facts) : ""].filter(Boolean).join(" · ")}
              {!unplayable && route === "transcode" && ` · ${t("player.converted")}${quality === "original" ? "" : ` ${quality}`}`}
            </div>
          </div>
        </div>
        {mode !== "bar" && (item.file || facts) && (
          <div className="flex flex-wrap items-center gap-1">
            {item.file ? (
              <>
                {fileChips(item.file).map((chip) => (
                  <Chip key={chip}>{chip}</Chip>
                ))}
                <TargetChip file={item.file} />
              </>
            ) : (
              facts && describeSource(facts) !== "" && <Chip>{describeSource(facts)}</Chip>
            )}
          </div>
        )}
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
        {isVideo && facts?.transcode_available && (
          <select
            aria-label={t("player.quality")}
            title={t("player.quality")}
            value={quality}
            onChange={(event) => chooseQuality(event.target.value as Quality)}
            className="shrink-0 self-start rounded border border-border-subtle bg-transparent px-1 py-0.5 text-xs"
          >
            {QUALITIES.map((choice) => (
              <option key={choice} value={choice} className="text-black">
                {choice === "original" ? t("player.quality.original") : choice}
              </option>
            ))}
          </select>
        )}
        {isVideo && tracks.length > 0 && (
          <select
            aria-label={t("player.subtitles")}
            title={t("player.subtitles")}
            value={subtitle ?? ""}
            onChange={(event) => setSubtitle(event.target.value === "" ? null : Number(event.target.value))}
            className="shrink-0 self-start rounded border border-border-subtle bg-transparent px-1 py-0.5 text-xs"
          >
            <option value="" className="text-black">{t("player.subtitles")}: {t("player.subtitles.off")}</option>
            {tracks.map((track) => (
              <option key={track.id} value={track.id} className="text-black">
                {subtitleLabel(track)}
              </option>
            ))}
          </select>
        )}
        <div className="flex shrink-0 items-center gap-1">
          <Button variant="icon" aria-label={muted ? t("player.unmute") : t("player.mute")} onClick={() => setMuted(!muted)}>
            {muted ? <VolumeX aria-hidden className="size-4" /> : <Volume2 aria-hidden className="size-4" />}
          </Button>
          <input
            type="range"
            aria-label={t("player.volume")}
            className="w-20 accent-accent"
            min={0}
            max={1}
            step={0.05}
            value={muted ? 0 : volume}
            onChange={(event) => changeVolume(Number(event.target.value))}
          />
        </div>
      </div>
      {mode === "bar" && <Modes mode={mode} onClose={player.close} />}
    </section>
  );
}

/** The page area beside the sidebar and above the status bar, which the floating modes stay inside. */
function useContentBox(): DOMRect | null {
  const [box, setBox] = useState<DOMRect | null>(null);
  useEffect(() => {
    const area = document.querySelector('[data-region="content"]');
    if (!area) return;
    const measure = () => setBox(area.getBoundingClientRect());
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(area);
    window.addEventListener("resize", measure);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", measure);
    };
  }, []);
  return box;
}

const FLOAT_GAP = 16;
const CINEMA_MAX_WIDTH = 1024;
const MINI_WIDTH = 576;

/** Mini and cinema sit in the content area's lower right corner; cinema is up to half its width, capped at the library's. */
function floatingStyle(mode: PlayerMode, box: DOMRect | null): React.CSSProperties | undefined {
  if (mode !== "mini" && mode !== "cinema") return undefined;
  const right = box ? window.innerWidth - box.right + FLOAT_GAP : FLOAT_GAP;
  const bottom = box ? window.innerHeight - box.bottom + FLOAT_GAP : FLOAT_GAP;
  const available = (box ? box.width : window.innerWidth) - 2 * FLOAT_GAP;
  const wanted = mode === "mini" ? MINI_WIDTH : Math.min(CINEMA_MAX_WIDTH, (box ? box.width : window.innerWidth) / 2);
  return { right, bottom, width: Math.max(0, Math.min(wanted, available)) };
}

function sourceUrl(route: PlaybackRoute | null, mediaObjectId: number, offset: number, quality: Quality): string | undefined {
  if (route === null) return undefined;
  return route === "direct" ? streamUrl(mediaObjectId) : transcodeUrl(mediaObjectId, offset, quality);
}

const VOLUME_KEY = "solarr.player.volume";

function storedVolume(): number {
  try {
    const stored = Number(localStorage.getItem(VOLUME_KEY));
    return Number.isFinite(stored) && stored > 0 && stored <= 1 ? stored : 1;
  } catch {
    return 1;
  }
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

const MODE_ICONS = { bar: PanelBottom, mini: PictureInPicture2, cinema: RectangleHorizontal, full: Maximize2 } as const;

/** Where the player sits, and closing it. */
function Modes({ mode, onClose }: { mode: PlayerMode; onClose: () => void }) {
  const player = usePlayer();
  return (
    <div className="flex shrink-0 items-center gap-0.5" role="group" aria-label={t("player.placement")}>
      {(["bar", "mini", "cinema", "full"] as const).map((choice) => {
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
