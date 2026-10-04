import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { mediaTypeName } from "@/lib/mediaTypeIcon";

import { LanguagePicker } from "./LanguagePicker";

type QualityTarget = components["schemas"]["QualityTarget"];

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
const smallButton = "rounded-md border border-border-subtle px-2.5 py-1 text-xs text-ink-muted hover:text-ink";

const AUDIO_MEDIA = new Set(["music", "audiobook", "podcast"]);
const MEDIA_TYPES = ["music", "tv", "movie", "audiobook", "podcast", "youtube", "news", "book", "comic", "rom", "adult"];

// ffprobe's own codec names, which is what the probe measures files by.
const AUDIO_CODECS = ["aac", "mp3", "opus", "vorbis", "flac", "alac"] as const;
const LOSSLESS = new Set(["flac", "alac"]);
const AUDIO_FILE_TYPES: [string, string][] = [["m4a", ".m4a"], ["mp3", ".mp3"], ["opus", ".opus"], ["ogg", ".ogg"], ["flac", ".flac"]];
const VIDEO_FILE_TYPES: [string, string][] = [
  ["mkv", "Matroska (.mkv)"], ["mp4", "MP4 (.mp4, .m4v)"], ["avi", "AVI (.avi)"], ["wmv", "Windows Media (.wmv)"], ["mpeg", "MPEG (.mpg, .mpeg)"],
  ["mov", "QuickTime (.mov)"], ["ts", "Transport stream (.ts, .m2ts)"], ["webm", "WebM (.webm)"], ["flv", "Flash video (.flv)"], ["ogm", "Ogg media (.ogm)"],
];
const RESOLUTIONS = ["2160p", "1080p", "720p", "576p", "480p"] as const;
const VIDEO_CODECS = ["h265", "h264", "av1", "vp9", "mpeg2"] as const;
const HDR_FORMATS = ["sdr", "hdr10", "hdr10plus", "dolby-vision", "hlg"] as const;
const VIDEO_AUDIO = ["aac", "eac3", "ac3", "dts", "truehd", "flac", "opus"] as const;
const CHANNEL_LAYOUTS = ["1.0", "2.0", "2.1", "5.1", "7.1"] as const;
const FRAME_RATES: [number, string][] = [[24, "24 (23.976)"], [25, "25"], [30, "30 (29.97)"], [50, "50"], [60, "60 (59.94)"]];

/** A new target's starting point: lossless audio, or a 1080p h265 picture. */
function blankTarget(mediaType: string): QualityTarget {
  return AUDIO_MEDIA.has(mediaType)
    ? { name: "", media_type: mediaType, audio_codec: "flac", container: "flac", tolerance_pct: 10 }
    : { name: "", media_type: mediaType, audio_codec: "", container: "", tolerance_pct: 0, resolution: "1080p", video_codec: "h265" };
}

function Pick({
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

function Num({
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
  return (
    <div className="flex flex-col gap-3">
      <Group title="Container">
        <Pick
          label="File type"
          value={target.container}
          options={audioOnly ? AUDIO_FILE_TYPES : VIDEO_FILE_TYPES}
          onChange={(container) => set({ container })}
          any={audioOnly ? null : "any"}
        />
      </Group>
      {!audioOnly && (
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
      {!audioOnly && (
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
        {!initial && (
          <Pick
            label="For"
            value={target.media_type}
            options={MEDIA_TYPES.map((type): [string, string] => [type, mediaTypeName(type)])}
            onChange={(type) => setTarget({ ...blankTarget(type), name: target.name })}
            any={null}
          />
        )}
      </div>
      <TargetFields target={target} onChange={setTarget} />
      <div className="flex items-center gap-2">
        <button type="button" disabled={save.isPending} onClick={() => save.mutate()} className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50">
          {save.isPending ? "Saving…" : "Save"}
        </button>
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
    queryFn: async () => {
      const { data, error } = await api.GET("/qualitytargets");
      if (error) throw error;
      return data;
    },
  });
  const remove = useMutation({
    mutationFn: async (id: number) => {
      const { error } = await api.DELETE("/qualitytargets/{id}", { params: { path: { id } } });
      if (error) throw error;
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
    <section className="flex flex-col gap-3">
      <div className="flex items-center gap-3">
        <h2 className="text-sm font-medium">Target qualities</h2>
        <button type="button" onClick={() => setEditing("new")} className="ml-auto flex items-center gap-1 rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted hover:text-ink">
          <Plus aria-hidden className="size-4" />
          New target
        </button>
      </div>
      <p className="text-xs text-ink-muted">
        The one quality you would pick if you could choose freely. A library picks one of these under its own Quality page; a file meets it when every part named matches what the probe measured.
      </p>
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
                    <span className="block truncate text-sm font-medium">{target.name}</span>
                    <span className="block truncate text-xs text-ink-muted">{summariseTarget(target)}</span>
                  </span>
                  <button type="button" onClick={() => setEditing(target.id ?? null)} className={smallButton}>
                    Edit
                  </button>
                  <button
                    type="button"
                    aria-label={`Remove target ${target.name}`}
                    disabled={remove.isPending}
                    onClick={() => target.id && remove.mutate(target.id)}
                    className="rounded-md border border-border-subtle p-1 text-ink-muted hover:text-danger disabled:opacity-50"
                  >
                    <Trash2 aria-hidden className="size-3.5" />
                  </button>
                </li>
              ),
            )}
          </ul>
        </div>
      ))}
    </section>
  );
}
