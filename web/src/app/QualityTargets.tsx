import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { mediaTypeName } from "@/lib/mediaTypeIcon";
import { useMediaTypes } from "@/lib/useMediaTypes";
import { Button, RemoveButton } from "@/ui/Button";
import { inputStyle } from "@/ui/form";

import { applyProfile } from "@/lib/targetProfile";

import { LanguagePicker } from "./LanguagePicker";
import { Card } from "./SettingsCard";

type QualityTarget = components["schemas"]["QualityTarget"];

const smallButton = "rounded-md border border-border-subtle px-2.5 py-1 text-xs text-ink-muted hover:text-ink";

const AUDIO_MEDIA = new Set(["music", "audiobook", "podcast"]);
const MEDIA_TYPES = ["music", "tv", "movie", "audiobook", "podcast", "youtube", "news", "book", "comic", "rom", "adult"];

// ffprobe's own codec names, which is what the probe measures files by.
export const AUDIO_CODECS = ["aac", "mp3", "opus", "vorbis", "flac", "alac"] as const;
const LOSSLESS = new Set(["flac", "alac"]);
const AUDIO_FILE_TYPES: [string, string][] = [["m4a", ".m4a"], ["m4b", ".m4b"], ["mp3", ".mp3"], ["opus", ".opus"], ["ogg", ".ogg"], ["flac", ".flac"]];
const DOCUMENT_FILE_TYPES: [string, string][] = [["epub", "EPUB (.epub)"], ["pdf", "PDF (.pdf)"], ["azw3", "Kindle (.azw3)"], ["mobi", "Mobi (.mobi)"], ["cbz", "Comic archive (.cbz)"]];
const DOCUMENT_MEDIA = new Set(["book", "comic"]);
export const ENCODER_PRESETS = ["ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow"] as const;
export const ENCODER_TUNES = ["animation", "grain", "film", "stillimage", "fastdecode", "zerolatency"] as const;
const AUDIO_CHANNEL_LAYOUTS = ["1.0", "2.0"] as const;
const SAMPLE_RATES: [number, string][] = [[44100, "44.1 kHz"], [48000, "48 kHz"], [88200, "88.2 kHz"], [96000, "96 kHz"], [192000, "192 kHz"]];

const CODEC_LABEL: Record<string, string> = { aac: "AAC", ac3: "AC3", eac3: "E-AC3", mp3: "MP3", flac: "FLAC", vorbis: "Vorbis", opus: "Opus", alac: "ALAC", dts: "DTS", truehd: "TrueHD" };
const CHANNEL_WORD: Record<string, string> = { "1.0": "mono", "2.0": "stereo" };
const VIDEO_CODEC_LABEL: Record<string, string> = { h265: "H.265", h264: "H.264", av1: "AV1", vp9: "VP9", mpeg2: "MPEG-2" };

/** A name from the specs, the way the shipped presets are named, so a changed number can be renamed in one click. */
export function nameFromSpecs(target: QualityTarget): string {
  const sound = (): string => {
    const codec = CODEC_LABEL[target.audio_codec] ?? target.audio_codec.toUpperCase();
    const channels = target.audio_channels ? (CHANNEL_WORD[target.audio_channels] ?? target.audio_channels) : "";
    return [codec, channels].filter(Boolean).join(" ");
  };
  if (DOCUMENT_MEDIA.has(target.media_type ?? "")) return (target.container ?? "").toUpperCase();
  if (AUDIO_MEDIA.has(target.media_type ?? "")) {
    const rate = target.sample_rate_hz ? `${target.sample_rate_hz / 1000} kHz` : "";
    const depth = target.bit_depth ? `${target.bit_depth}-bit` : "";
    const rateOfBits = target.bitrate_kbps ? `${target.bitrate_kbps} kbit/s` : "";
    return [sound(), rate, depth, rateOfBits, (target.container ?? "").toUpperCase(), target.chapters ? "chapters" : ""].filter(Boolean).join(", ");
  }
  const fps = target.frame_rate ? `${target.frame_rate} fps` : target.max_frame_rate ? `source fps (max ${target.max_frame_rate})` : "source fps";
  const codec = [VIDEO_CODEC_LABEL[target.video_codec ?? ""] ?? target.video_codec?.toUpperCase(), target.bit_depth === 10 ? "10-bit" : ""].filter(Boolean).join(" ");
  const rate = [target.video_bitrate_kbps ? `${target.video_bitrate_kbps.toLocaleString("en-US")} kbit/s` : "", target.crf ? `CRF ${target.crf}` : ""].filter(Boolean).join(" or ");
  const audio = [sound(), target.audio_bitrate_kbps ? `${target.audio_bitrate_kbps} kbit/s` : ""].filter(Boolean).join(" ");
  return [target.resolution, fps, codec, (target.container ?? "").toUpperCase(), audio, rate, target.encoder_preset].filter(Boolean).join(", ");
}
const VIDEO_FILE_TYPES: [string, string][] = [
  ["mkv", "Matroska (.mkv)"], ["mp4", "MP4 (.mp4, .m4v)"], ["avi", "AVI (.avi)"], ["wmv", "Windows Media (.wmv)"], ["mpeg", "MPEG (.mpg, .mpeg)"],
  ["mov", "QuickTime (.mov)"], ["ts", "Transport stream (.ts, .m2ts)"], ["webm", "WebM (.webm)"], ["flv", "Flash video (.flv)"], ["ogm", "Ogg media (.ogm)"],
];
export const RESOLUTIONS = ["2160p", "1080p", "720p", "576p", "480p"] as const;
export const VIDEO_CODECS = ["h265", "h264", "av1", "vp9", "mpeg2"] as const;
const HDR_FORMATS = ["sdr", "hdr10", "hdr10plus", "dolby-vision", "hlg"] as const;
export const VIDEO_AUDIO = ["aac", "eac3", "ac3", "dts", "truehd", "flac", "opus"] as const;
const CHANNEL_LAYOUTS = ["1.0", "2.0", "2.1", "5.1", "7.1"] as const;
const FRAME_RATES: [number, string][] = [[24, "24 (23.976)"], [25, "25"], [30, "30 (29.97)"], [50, "50"], [60, "60 (59.94)"]];

/** A new target's starting point: lossless audio, or a 1080p h265 picture. */
function blankTarget(mediaType: string): QualityTarget {
  if (DOCUMENT_MEDIA.has(mediaType)) return { name: "", media_type: mediaType, audio_codec: "", container: "epub", tolerance_pct: 0 };
  return AUDIO_MEDIA.has(mediaType)
    ? { name: "", media_type: mediaType, audio_codec: "flac", container: "flac", tolerance_pct: 10 }
    : { name: "", media_type: mediaType, audio_codec: "", container: "", tolerance_pct: 0, resolution: "1080p", video_codec: "h265" };
}

export function Pick({
  label, value, options, onChange, any = "any",
}: {
  label: string;
  value: string | undefined;
  options: readonly (string | [string, string])[];
  onChange: (value: string) => void;
  any?: string | null;
}) {
  return (
    <label className="flex flex-col gap-1 text-ink-muted">
      {label}
      <select value={value ?? ""} onChange={(event) => onChange(event.target.value)} className={inputStyle}>
        {any !== null && <option value="">{any}</option>}
        {options.map((option) => {
          const [key, text] = typeof option === "string" ? [option, option] : option;
          return (
            <option key={key} value={key}>
              {text}
            </option>
          );
        })}
      </select>
    </label>
  );
}

export function Num({
  label, value, onChange, hint, width = "w-28", step,
}: {
  label: string;
  value: number | undefined;
  onChange: (value: number | undefined) => void;
  hint?: string;
  width?: string;
  step?: number;
}) {
  return (
    <label className="flex flex-col gap-1 text-ink-muted" title={hint}>
      {label}
      <input
        type="number"
        min={0}
        step={step}
        value={value ?? ""}
        placeholder="any"
        onChange={(event) => onChange(event.target.value === "" ? undefined : Number(event.target.value))}
        className={`${inputStyle} ${width}`}
      />
    </label>
  );
}

function Group({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <fieldset className="flex flex-col gap-2 rounded-md border border-border-subtle p-3">
      <legend className="px-1 text-xs font-medium text-ink">{title}</legend>
      <div className="flex flex-wrap items-end gap-3 text-xs">{children}</div>
    </fieldset>
  );
}

/** One target's fields in the order a file is read: its container, then the picture, then the sound, then subtitles. */
function TargetFields({ target, onChange }: { target: QualityTarget; onChange: (target: QualityTarget) => void }) {
  const set = (change: Partial<QualityTarget>) => onChange({ ...target, ...change });
  const audioOnly = AUDIO_MEDIA.has(target.media_type ?? "");
  const documentOnly = DOCUMENT_MEDIA.has(target.media_type ?? "");
  return (
    <div className="flex flex-col gap-3">
      <Group title="Container">
        <Pick
          label="File type"
          value={target.container}
          options={documentOnly ? DOCUMENT_FILE_TYPES : audioOnly ? AUDIO_FILE_TYPES : VIDEO_FILE_TYPES}
          onChange={(container) => set({ container })}
          any={audioOnly || documentOnly ? null : "any"}
        />
      </Group>
      <Group title="Over and under the target">
        <label className="flex items-start gap-2 text-sm" title="A release above the target ranks before one below it, however near the lower one is">
          <input type="checkbox" className="mt-1" checked={target.prefer_over ?? false} onChange={(event) => set({ prefer_over: event.target.checked })} />
          <span>
            Prefer over
            <span className="block text-xs text-ink-muted">A download above the target beats one below it, because transcoding can bring quality down, never up. 4k beats 720p for a 1080p target, and a 6 Mbit/s release beats a 1.5 Mbit/s one for 1.6.</span>
          </span>
        </label>
        <label className="flex items-start gap-2 text-sm" title="Everything below the target only wins when nothing at or above it exists">
          <input type="checkbox" className="mt-1" checked={target.ignore_below ?? false} onChange={(event) => set({ ignore_below: event.target.checked })} />
          <span>
            Ignore everything below the target while anything at or above it exists
            <span className="block text-xs text-ink-muted">A 720p release is chosen only when no 1080p or better one is found.</span>
          </span>
        </label>
      </Group>
      {!audioOnly && !documentOnly && (
        <Group title="Video">
          <Pick label="Resolution" value={target.resolution} options={RESOLUTIONS} onChange={(resolution) => set({ resolution })} />
          <Pick label="Video codec" value={target.video_codec} options={VIDEO_CODECS} onChange={(video_codec) => set({ video_codec })} />
          <Pick label="HDR" value={target.hdr} options={HDR_FORMATS} onChange={(hdr) => set({ hdr })} />
          <Pick
            label="Frame rate"
            value={target.frame_rate === undefined ? "" : String(target.frame_rate)}
            options={FRAME_RATES.map(([rate, text]): [string, string] => [String(rate), text])}
            onChange={(rate) => set({ frame_rate: rate === "" ? undefined : Number(rate) })}
          />
          <Num label="Frame rate cap" value={target.max_frame_rate || undefined} width="w-24" onChange={(max_frame_rate) => set({ max_frame_rate })} hint="When the frame rate follows the source, never above this" />
          <Pick label="Colour depth" value={target.bit_depth ? String(target.bit_depth) : ""} options={["8", "10"]} onChange={(depth) => set({ bit_depth: depth === "" ? undefined : Number(depth) })} />
          <Pick label="Encoder preset" value={target.encoder_preset} options={ENCODER_PRESETS} onChange={(encoder_preset) => set({ encoder_preset })} />
          <Pick label="Encoder tune" value={target.encoder_tune} options={ENCODER_TUNES} onChange={(encoder_tune) => set({ encoder_tune })} />
          <label className="flex items-center gap-2 text-sm" title="An encode of this quality deinterlaces; an interlaced file counts as short of it">
            <input type="checkbox" checked={target.deinterlace ?? false} onChange={(event) => set({ deinterlace: event.target.checked })} />
            Deinterlace
          </label>
          <Num label="Video bitrate (kbit/s)" value={target.video_bitrate_kbps} onChange={(video_bitrate_kbps) => set({ video_bitrate_kbps })} hint="A release's size over its length stands in before download" />
          <Num
            label="CRF"
            value={target.crf || undefined}
            step={0.5}
            width="w-20"
            onChange={(crf) => set({ crf })}
            hint="The constant rate factor an x264 or x265 encode states in its settings; lower is better. A file at or below it meets it; a file that states none is not held against it."
          />
        </Group>
      )}
      {!documentOnly && (
      <Group title="Audio">
        {audioOnly ? (
          <Pick
            label="Codec"
            value={target.audio_codec}
            options={AUDIO_CODECS}
            onChange={(codec) => set({ audio_codec: codec, bitrate_kbps: LOSSLESS.has(codec) ? undefined : (target.bitrate_kbps ?? 256) })}
            any={null}
          />
        ) : (
          <>
            <Pick label="Codec" value={target.audio_codec} options={VIDEO_AUDIO} onChange={(audio_codec) => set({ audio_codec })} />
            <Pick label="Channels" value={target.audio_channels} options={CHANNEL_LAYOUTS} onChange={(audio_channels) => set({ audio_channels })} />
            <Num label="Audio bitrate (kbit/s)" value={target.audio_bitrate_kbps} onChange={(audio_bitrate_kbps) => set({ audio_bitrate_kbps })} hint="Within the tolerance of this" />
          </>
        )}
        {audioOnly && <Pick label="Channels" value={target.audio_channels} options={AUDIO_CHANNEL_LAYOUTS.map((layout): [string, string] => [layout, CHANNEL_WORD[layout] ?? layout])} onChange={(audio_channels) => set({ audio_channels })} />}
        {audioOnly && target.media_type === "music" && (
          <>
            <Pick
              label="Sample rate"
              value={target.sample_rate_hz ? String(target.sample_rate_hz) : ""}
              options={SAMPLE_RATES.map(([rate, text]): [string, string] => [String(rate), text])}
              onChange={(rate) => set({ sample_rate_hz: rate === "" ? undefined : Number(rate) })}
            />
            <Pick label="Bit depth" value={target.bit_depth ? String(target.bit_depth) : ""} options={["16", "24", "32"]} onChange={(depth) => set({ bit_depth: depth === "" ? undefined : Number(depth) })} />
          </>
        )}
        {target.media_type === "audiobook" && (
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={target.chapters ?? false} onChange={(event) => set({ chapters: event.target.checked })} />
            Chapter marks
          </label>
        )}
        {audioOnly && !LOSSLESS.has(target.audio_codec) && (
          <Num label="Bitrate (kbit/s)" value={target.bitrate_kbps} onChange={(bitrate_kbps) => set({ bitrate_kbps })} />
        )}
        <Num
          label="Tolerance (±%)"
          value={target.tolerance_pct || undefined}
          width="w-20"
          onChange={(tolerance_pct) => set({ tolerance_pct: tolerance_pct ?? 0 })}
          hint="How far a bitrate may be from the target; 25 when empty for video"
        />
        {!audioOnly && (
          <LanguagePicker
            label="Audio language (any of)"
            help="Read from the file's tags; a file that states none is not held against it."
            value={target.audio_languages ?? []}
            onChange={(audio_languages) => set({ audio_languages })}
          />
        )}
      </Group>
      )}
      {!audioOnly && !documentOnly && (
        <Group title="Subtitles">
          <LanguagePicker
            label="Subtitle language (any of)"
            help="Embedded tracks and sidecar files count."
            value={target.subtitle_languages ?? []}
            onChange={(subtitle_languages) => set({ subtitle_languages })}
          />
        </Group>
      )}
    </div>
  );
}

function TargetForm({ initial, onDone, onCancel }: { initial?: QualityTarget; onDone: () => void; onCancel: () => void }) {
  const [target, setTarget] = useState<QualityTarget>(initial ?? blankTarget("tv"));
  const mediaTypes = useMediaTypes();
  const profiles = useQuery({ queryKey: ["targetProfiles"], queryFn: () => ok(api.GET("/targetprofiles")) });
  const targets = useQuery({ queryKey: ["qualityTargets"], queryFn: () => ok(api.GET("/qualitytargets")) });
  const fitting = (profiles.data ?? []).filter((profile) => profile.media_types.includes(target.media_type ?? ""));
  const save = useMutation({
    mutationFn: async () => {
      const { error } = initial?.id
        ? await api.PUT("/qualitytargets/{id}", { params: { path: { id: initial.id } }, body: target })
        : await api.POST("/qualitytargets", { body: target });
      if (error) throw error;
    },
    onSuccess: onDone,
  });
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-canvas p-3">
      <div className="flex flex-wrap items-end gap-3 text-xs">
        <label className="flex flex-col gap-1 text-ink-muted">
          Name
          <input value={target.name ?? ""} onChange={(event) => setTarget({ ...target, name: event.target.value })} className={`${inputStyle} w-56`} />
        </label>
        <label className="flex flex-col gap-1 text-ink-muted">
          Label
          <input value={target.label ?? ""} placeholder="e.g. Space conscious" onChange={(event) => setTarget({ ...target, label: event.target.value })} className={`${inputStyle} w-44`} />
        </label>
        <button type="button" onClick={() => setTarget({ ...target, name: nameFromSpecs(target) })} className={smallButton} title="Name it after what it asks for">
          Name from specs
        </button>
        {fitting.length > 0 && (
          <>
            <Pick
              label="Content profile"
              value={target.profile_id === undefined ? "" : String(target.profile_id)}
              options={fitting.map((profile): [string, string] => [String(profile.id), profile.name])}
              onChange={(id) => setTarget({ ...target, profile_id: id === "" ? undefined : Number(id) })}
              any="none"
            />
            <button
              type="button"
              disabled={target.profile_id === undefined}
              onClick={() => {
                const profile = fitting.find((candidate) => candidate.id === target.profile_id);
                if (profile) setTarget(applyProfile(target, profile, targets.data ?? []));
              }}
              className={smallButton}
              title="Fill in the settings that suit the profile; you can change every one afterwards"
            >
              Apply its recommended settings
            </button>
          </>
        )}
        {!initial && (
          <Pick
            label="For"
            value={target.media_type}
            options={mediaTypes.map((type): [string, string] => [type.key, type.label])}
            onChange={(type) => setTarget({ ...blankTarget(type), name: target.name })}
            any={null}
          />
        )}
      </div>
      <label className="flex flex-col gap-1 text-xs text-ink-muted">
        Description
        <input value={target.description ?? ""} onChange={(event) => setTarget({ ...target, description: event.target.value })} className={inputStyle} />
      </label>
      <TargetFields target={target} onChange={setTarget} />
      <div className="flex items-center gap-2">
        <Button variant="primary" type="button" disabled={save.isPending} onClick={() => save.mutate()}>
          {save.isPending ? "Saving…" : "Save"}
        </Button>
        <button type="button" onClick={onCancel} className={smallButton}>
          Cancel
        </button>
        {save.isError && <span className="whitespace-pre-wrap text-xs text-danger">{(save.error as { detail?: string }).detail ?? "That target could not be saved."}</span>}
      </div>
    </div>
  );
}

/** What a target asks for, in a line. */
export function summariseTarget(target: QualityTarget): string {
  const parts = [
    target.container ? `.${target.container}` : "",
    target.resolution,
    target.video_codec?.toUpperCase(),
    target.hdr?.toUpperCase(),
    target.crf ? `CRF ${target.crf}` : "",
    target.audio_codec ? target.audio_codec.toUpperCase() : "",
    target.bitrate_kbps ? `${target.bitrate_kbps} kbit/s` : "",
    target.audio_channels,
    target.audio_languages?.length ? `audio ${target.audio_languages.join("/")}` : "",
  ];
  return parts.filter(Boolean).join(" · ") || "Anything";
}

/** The shared target qualities: made here, picked by a library under its own Quality page. */
export function QualityTargetsSection() {
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState<number | "new" | null>(null);
  const targets = useQuery({
    queryKey: ["qualityTargets"],
    queryFn: () => ok(api.GET("/qualitytargets")),
  });
  const remove = useMutation({
    mutationFn: async (id: number) => {
      await ok(api.DELETE("/qualitytargets/{id}", { params: { path: { id } } }));
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["qualityTargets"] }),
  });
  const done = () => {
    setEditing(null);
    void queryClient.invalidateQueries({ queryKey: ["qualityTargets"] });
    void queryClient.invalidateQueries({ queryKey: ["libraryQuality"] });
  };
  const groups = MEDIA_TYPES.map((mediaType) => ({ mediaType, targets: (targets.data ?? []).filter((target) => target.media_type === mediaType) })).filter(
    (group) => group.targets.length > 0,
  );
  return (
    <Card
      title="Target qualities"
      help="The one quality you would pick if you could choose freely. A library picks one of these under its own Quality page; a file meets it when every part named matches what the probe measured."
      toggle={
        <button type="button" onClick={() => setEditing("new")} className="flex shrink-0 items-center gap-1 rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted hover:text-ink">
          <Plus aria-hidden className="size-4" />
          New target
        </button>
      }
    >
      {remove.isError && <p className="text-xs text-danger">{(remove.error as { detail?: string }).detail ?? "That target could not be removed."}</p>}
      {editing === "new" && <TargetForm onDone={done} onCancel={() => setEditing(null)} />}
      {targets.data && groups.length === 0 && editing !== "new" && <p className="text-xs text-ink-muted">No targets yet.</p>}
      {groups.map((group) => (
        <div key={group.mediaType} className="flex flex-col gap-2">
          <h3 className="text-xs font-medium text-ink-muted">{mediaTypeName(group.mediaType)}</h3>
          <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
            {group.targets.map((target) =>
              editing === target.id ? (
                <li key={target.id} className="p-3">
                  <TargetForm initial={target} onDone={done} onCancel={() => setEditing(null)} />
                </li>
              ) : (
                <li key={target.id} className="flex items-center gap-3 px-3 py-2.5">
                  <span className="min-w-0 flex-1">
                    <span className="flex items-baseline gap-2 text-sm font-medium">
                      <span className="truncate">{target.name}</span>
                      {target.label && <span className="shrink-0 rounded-full bg-canvas px-2 py-0.5 text-xs font-normal text-ink-muted">{target.label}</span>}
                    </span>
                    <span className="block truncate text-xs text-ink-muted">{summariseTarget(target)}</span>
                  </span>
                  <button type="button" onClick={() => setEditing(target.id ?? null)} className={smallButton}>
                    Edit
                  </button>
                  <RemoveButton
                    aria-label={`Remove target ${target.name}`}
                    disabled={remove.isPending}
                    onClick={() => target.id && remove.mutate(target.id)}
                  />
                </li>
              ),
            )}
          </ul>
        </div>
      ))}
    </Card>
  );
}
