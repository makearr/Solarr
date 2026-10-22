import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Plus, Trash2, X } from "lucide-react";

import { api, ok } from "@/api/client";
import { LanguageProfiles } from "./LanguageProfiles";
import { Card } from "./SettingsCard";
import { QualityTargetsSection } from "./QualityTargets";
import type { components } from "@/api/schema";
import { mediaTypeName } from "@/lib/mediaTypeIcon";
import { useMediaTypes } from "@/lib/useMediaTypes";
import {
  buildAudioRules, buildVideoRules, containerOf, parseAudioRules, parseVideoRules, summarise, type AudioRules, type Range, type VideoRules,
} from "@/lib/qualityRules";
import { useQualityProfiles } from "@/lib/resources";
import { isTierVisible, useTier } from "@/lib/useTier";
import { labelStyle } from "@/ui/form";

import { LanguagePicker } from "./LanguagePicker";

type QualityProfile = components["schemas"]["QualityProfile"];

// Every media type a stored profile may carry, for grouping; a new profile picks from the registered ones.
const MEDIA_TYPES = ["music", "audiobook", "podcast", "tv", "movie", "youtube", "news", "book", "comic", "rom", "adult"];
const AUDIO = new Set(["music", "audiobook", "podcast"]);
const VIDEO = new Set(["tv", "movie"]);

const RESOLUTIONS: Record<string, string> = {
  "2160p": "2160p (4K)", "1080p": "1080p", "1080i": "1080i", "720p": "720p", "576p": "576p", "480p": "480p", "360p": "360p",
};
const SOURCES: Record<string, string> = {
  remux: "Remux", bluray: "Blu-ray", webdl: "WEB-DL", webrip: "WEBRip", hdtv: "HDTV", dvd: "DVD", sdtv: "SDTV",
};
const VIDEO_CODECS: Record<string, string> = { h265: "H.265 (HEVC)", av1: "AV1", h264: "H.264 (AVC)", vp9: "VP9", mpeg2: "MPEG-2", xvid: "Xvid", divx: "DivX" };
const HDR_KINDS: Record<string, string> = {
  "dolby-vision": "Dolby Vision", hdr10plus: "HDR10+", hdr10: "HDR10", hdr: "HDR", hlg: "HLG",
};
const VIDEO_AUDIO_CODECS: Record<string, string> = {
  truehd: "TrueHD", dts: "DTS", eac3: "E-AC-3 (Dolby Digital Plus)", ac3: "AC-3 (Dolby Digital)", aac: "AAC", opus: "Opus", mp3: "MP3",
};
const VIDEO_CHANNELS: Record<string, string> = { "7.1": "7.1", "5.1": "Surround (5.1)", "2.0": "Stereo (2.0)", "1.0": "Mono (1.0)" };
// File types by the container format they stand for (an .m4v is MP4, a .wmv is ASF).
const VIDEO_CONTAINERS: Record<string, string> = {
  matroska: "Matroska (.mkv, .webm)", mp4: "MP4 (.mp4, .m4v, .mov)", avi: "AVI (.avi)", asf: "Windows Media (.wmv)", mpeg: "MPEG (.mpg, .mpeg, .vob)",
  mpegts: "Transport stream (.ts, .m2ts)", flv: "Flash video (.flv)", ogg: "Ogg (.ogm)",
};

const CODECS: Record<string, string> = {
  flac: "FLAC", alac: "ALAC", wav: "WAV", ape: "APE", opus: "Opus", vorbis: "Vorbis", ogg: "Ogg",
  aac: "AAC", mp3: "MP3",
};
const LOSSLESS = new Set(["flac", "alac", "wav", "ape"]);
const EXTENSIONS: Record<string, string> = {
  flac: ".flac", m4a: ".m4a", mp3: ".mp3", ogg: ".ogg", opus: ".opus", wav: ".wav", aac: ".aac", wma: ".wma", m4b: ".m4b",
};
// What the file holds, whatever it is called: an .m4a and an .mp4 are both MP4.
const CONTAINERS: Record<string, string> = {
  flac: "FLAC", mp4: "MP4 (.m4a, .m4b)", mp3: "MPEG audio (.mp3)", ogg: "Ogg (.ogg, .opus)", wav: "WAV", asf: "ASF (.wma)", aac: "AAC stream",
};
const CHANNELS: Record<string, string> = { "2.0": "Stereo (2.0)", "5.1": "Surround (5.1)", "1.0": "Mono (1.0)" };

// What migration 0012 ships every profile with.
const DEFAULT_UPGRADE = '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}';

/** Quality profiles, shared by every library of their media type (2026-09-25): a library only picks
 *  and orders them. Grouped by media type; audio profiles are built with a form. */
const SAMPLE_RATE_FALLBACK = { min: "44100", ideal: "48000", max: "96000" };

export function QualityProfilesPage() {
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState<number | "new" | null>(null);
  const profiles = useQualityProfiles();
  const remove = useMutation({
    mutationFn: async (id: number) => {
      await ok(api.DELETE("/qualityprofiles/{id}", { params: { path: { id } } }));
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["qualityProfiles"] }),
  });
  const done = () => {
    setEditing(null);
    void queryClient.invalidateQueries({ queryKey: ["qualityProfiles"] });
  };

  const groups = MEDIA_TYPES.map((mediaType) => ({
    mediaType,
    profiles: (profiles.data ?? []).filter((profile) => profile.media_type === mediaType),
  })).filter((group) => group.profiles.length > 0);

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-4 p-6">
      <h1 className="text-lg font-semibold">Quality profiles</h1>
      <Card
        title="Profiles"
        help="What each kind of file is worth. A library lists the profiles it accepts, in order, under its own Quality page; the same profile can serve several libraries."
        toggle={
          <button
            type="button"
            onClick={() => setEditing("new")}
            className="flex shrink-0 items-center gap-1 rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted hover:text-ink"
          >
            <Plus aria-hidden className="size-4" />
            New profile
          </button>
        }
      >
      {remove.isError && (
        <p className="text-xs text-danger">
          {(remove.error as { detail?: string }).detail ?? "That profile could not be removed."}
        </p>
      )}
      {editing === "new" && <ProfileForm onDone={done} onCancel={() => setEditing(null)} />}
      {profiles.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}

      {groups.map((group) => (
        <section key={group.mediaType} className="flex flex-col gap-2">
          <h3 className="text-xs font-medium text-ink-muted">{mediaTypeName(group.mediaType)}</h3>
          <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
            {group.profiles.map((profile) =>
              editing === profile.id ? (
                <li key={profile.id} className="p-3">
                  <ProfileForm initial={profile} onDone={done} onCancel={() => setEditing(null)} />
                </li>
              ) : (
                <li key={profile.id} className="flex items-center gap-3 px-3 py-2.5">
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium">{profile.name}</span>
                    <span className="block truncate text-xs text-ink-muted">{summarise(profile.rules)}</span>
                  </span>
                  <button type="button" onClick={() => setEditing(profile.id)} className={smallButton}>
                    Edit
                  </button>
                  <button
                    type="button"
                    aria-label={`Remove quality profile ${profile.name}`}
                    disabled={remove.isPending}
                    onClick={() => remove.mutate(profile.id)}
                    className="rounded-md border border-border-subtle p-1 text-ink-muted hover:text-danger disabled:opacity-50"
                  >
                    <Trash2 aria-hidden className="size-3.5" />
                  </button>
                </li>
              ),
            )}
          </ul>
        </section>
      ))}
      </Card>
      <QualityTargetsSection />
      <LanguageProfiles />
    </div>
  );
}

/** One profile's editor: the form for audio, the rules as JSON for the rest and at expert. */
function ProfileForm({
  initial,
  onDone,
  onCancel,
}: {
  initial?: QualityProfile;
  onDone: () => void;
  onCancel: () => void;
}) {
  const tier = useTier();
  const [name, setName] = useState(initial?.name ?? "");
  const [mediaType, setMediaType] = useState(initial?.media_type ?? "music");
  // An existing profile keeps its own type even when this build has no module for it.
  const pickable = [...new Set([...useMediaTypes().map((type): string => type.key), mediaType])];
  // A new profile starts with the sample rate target on (asked 2026-09-30); an existing one keeps its own.
  const [rules, setRules] = useState<AudioRules>(() =>
    initial ? parseAudioRules(initial.rules) : { ...parseAudioRules(""), sampleRate: SAMPLE_RATE_FALLBACK },
  );
  const [video, setVideo] = useState<VideoRules>(() => parseVideoRules(initial && VIDEO.has(initial.media_type) ? initial.rules : ""));
  const [json, setJson] = useState(initial?.rules ?? "");
  const [asJSON, setAsJSON] = useState(false);
  const [upgrade, setUpgrade] = useState(initial?.upgrade ?? DEFAULT_UPGRADE);
  const isVideo = VIDEO.has(mediaType);
  const hasForm = AUDIO.has(mediaType) || isVideo;
  const built = () => (isVideo ? buildVideoRules(initial?.rules ?? "", name, video) : buildAudioRules(initial?.rules ?? "", name, rules));

  const save = useMutation({
    mutationFn: async () => {
      const body = {
        name,
        rules: hasForm && !asJSON ? built() : json,
        upgrade,
        media_type: mediaType,
      };
      const { error } = initial
        ? await api.PATCH("/qualityprofiles/{id}", { params: { path: { id: initial.id } }, body })
        : await api.POST("/qualityprofiles", { body });
      if (error) throw error;
    },
    onSuccess: onDone,
  });

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
      className="flex flex-col gap-4 rounded-lg border border-border-subtle bg-surface p-4 text-sm"
    >
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex min-w-48 flex-1 flex-col gap-1 text-xs text-ink-muted">
          Name
          <input required value={name} onChange={(event) => setName(event.target.value)} className={input} />
        </label>
        <label className={labelStyle}>
          Media type
          <select
            value={mediaType}
            disabled={initial !== undefined}
            onChange={(event) => setMediaType(event.target.value)}
            className={input}
          >
            {pickable.map((type) => (
              <option key={type} value={type}>
                {mediaTypeName(type)}
              </option>
            ))}
          </select>
        </label>
      </div>

      {hasForm && !asJSON && !isVideo && <AudioRulesForm rules={rules} onChange={setRules} />}
      {hasForm && !asJSON && isVideo && <VideoRulesForm rules={video} onChange={setVideo} />}
      {(!hasForm || asJSON) && (
        <label className={labelStyle}>
          {hasForm ? "Rules (JSON)" : `${mediaTypeName(mediaType)} is not built yet, so its rules are written as JSON.`}
          <textarea value={json} onChange={(event) => setJson(event.target.value)} className={`${input} h-40 font-mono`} />
        </label>
      )}
      {isTierVisible(tier, "expert") && (
        <details className="text-xs text-ink-muted">
          <summary className="cursor-pointer">Expert</summary>
          <div className="mt-2 flex flex-col gap-2">
            {hasForm && (
              <label className="flex items-center gap-2">
                <input
                  type="checkbox"
                  checked={asJSON}
                  onChange={(event) => {
                    if (event.target.checked) setJson(built());
                    else if (isVideo) setVideo(parseVideoRules(json));
                    else setRules(parseAudioRules(json));
                    setAsJSON(event.target.checked);
                  }}
                />
                Edit the rules as JSON
              </label>
            )}
            <label className="flex flex-col gap-1">
              Upgrades (JSON)
              <textarea value={upgrade} onChange={(event) => setUpgrade(event.target.value)} className={`${input} h-16 font-mono`} />
            </label>
          </div>
        </details>
      )}

      {save.isError && (
        <p className="text-xs text-danger">{(save.error as { detail?: string }).detail ?? "The profile could not be saved."}</p>
      )}
      <div className="flex gap-2">
        <button type="submit" disabled={save.isPending} className="rounded-md bg-accent px-3 py-1.5 text-accent-ink disabled:opacity-50">
          Save
        </button>
        <button type="button" onClick={onCancel} className={smallButton}>
          Cancel
        </button>
      </div>
    </form>
  );
}

/** A video profile's parts: what it prefers, in order, what it never accepts and which languages and groups it wants. */
function VideoRulesForm({ rules, onChange }: { rules: VideoRules; onChange: (rules: VideoRules) => void }) {
  const set = (patch: Partial<VideoRules>) => onChange({ ...rules, ...patch });
  const order = Object.keys(RESOLUTIONS);
  const [typed, setTyped] = useState("");
  const addGroups = () => {
    const names = typed.split(",").map((name) => name.trim()).filter((name) => name !== "" && !rules.preferredGroups.includes(name));
    if (names.length > 0) set({ preferredGroups: [...rules.preferredGroups, ...names] });
    setTyped("");
  };
  return (
    <div className="flex flex-col gap-4">
      <RankedList
        label="Resolution, best first"
        help="Equal tiers rank together. A resolution not listed is accepted but ranks last."
        options={RESOLUTIONS}
        value={rules.resolutions}
        onChange={(resolutions) => set({ resolutions })}
      />
      <fieldset className="flex flex-wrap items-end gap-3 text-xs">
        <legend className="mb-1 font-medium">Accept only resolutions between</legend>
        <label className="flex flex-col gap-1 text-ink-muted">
          Lowest
          <select
            value={rules.minResolution}
            className={input}
            onChange={(event) => set({ minResolution: event.target.value, maxResolution: event.target.value && !rules.maxResolution ? order[0]! : rules.maxResolution })}
          >
            <option value="">No limit</option>
            {[...order].reverse().map((name) => (
              <option key={name} value={name}>
                {RESOLUTIONS[name]}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-ink-muted">
          Highest
          <select
            value={rules.maxResolution}
            className={input}
            onChange={(event) => set({ maxResolution: event.target.value, minResolution: event.target.value && !rules.minResolution ? order[order.length - 1]! : rules.minResolution })}
          >
            <option value="">No limit</option>
            {order.map((name) => (
              <option key={name} value={name}>
                {RESOLUTIONS[name]}
              </option>
            ))}
          </select>
        </label>
        {(rules.minResolution || rules.maxResolution) && (
          <button type="button" className={smallButton} onClick={() => set({ minResolution: "", maxResolution: "" })}>
            No limit
          </button>
        )}
      </fieldset>
      <RankedList label="Source, best first" options={SOURCES} value={rules.sources} onChange={(sources) => set({ sources })} />
      <RankedList label="Video codec, best first" options={VIDEO_CODECS} value={rules.videoCodecs} onChange={(videoCodecs) => set({ videoCodecs })} />
      <Chips label="Never accept these video codecs" options={VIDEO_CODECS} value={rules.rejectCodecs} onChange={(rejectCodecs) => set({ rejectCodecs })} />
      <RankedList label="HDR, best first" options={HDR_KINDS} value={rules.hdr} onChange={(hdr) => set({ hdr })} />
      <RankedList label="Audio codec, best first" options={VIDEO_AUDIO_CODECS} value={rules.audioCodecs} onChange={(audioCodecs) => set({ audioCodecs })} />
      <RankedList label="Audio channels, best first" options={VIDEO_CHANNELS} value={rules.audioChannels} onChange={(audioChannels) => set({ audioChannels })} />
      <RankedList
        label="File type, best first"
        help="What the file holds, whatever it is called. A type not listed is accepted but ranks last."
        options={VIDEO_CONTAINERS}
        value={rules.containers}
        onChange={(containers) => set({ containers })}
      />
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-1 text-xs font-medium">Aim for (the nearest ranks first, above or below)</legend>
        <OptionalRange label="Video bitrate (kbit/s)" value={rules.videoBitrate} fallback={{ min: "2000", ideal: "4000", max: "8000" }} onChange={(videoBitrate) => set({ videoBitrate })} />
        <OptionalRange label="Audio bitrate (kbit/s)" value={rules.audioBitrate} fallback={{ min: "128", ideal: "384", max: "640" }} onChange={(audioBitrate) => set({ audioBitrate })} />
        <OptionalRange label="Frame rate (frames a second)" value={rules.frameRate} fallback={{ min: "", ideal: "24", max: "" }} onChange={(frameRate) => set({ frameRate })} />
        <p className="text-xs text-ink-muted">
          A release shows no bitrate before download, so its size over the episode's length stands in for it; the probe measures the real one afterwards.
        </p>
      </fieldset>
      <LanguagePicker
        label="Prefer files with these audio languages"
        help="A tie-breaker between equal releases, never a gate."
        value={rules.languages.audio}
        onChange={(audio) => set({ languages: { ...rules.languages, audio } })}
      />
      <LanguagePicker
        label="Prefer files with these subtitle languages"
        help="Embedded tracks and sidecar files count once the file is probed."
        value={rules.languages.subtitle}
        onChange={(subtitle) => set({ languages: { ...rules.languages, subtitle } })}
      />
      <LanguagePicker
        label="Accept only files with one of these audio languages"
        help="Read from the file once it is probed; a release's name alone never decides. Empty accepts any."
        value={rules.audioLanguages}
        onChange={(audioLanguages) => set({ audioLanguages })}
      />
      <fieldset className="flex flex-col gap-1.5">
        <legend className="text-xs font-medium">Preferred release groups</legend>
        <p className="text-xs text-ink-muted">A release from one of these ranks above an otherwise equal one.</p>
        <div className="flex flex-wrap items-center gap-1 text-xs">
          {rules.preferredGroups.map((group) => (
            <span key={group} className="flex items-center gap-1 rounded-full border border-border-subtle px-2 py-0.5 text-ink">
              {group}
              <button
                type="button"
                aria-label={`Remove ${group}`}
                onClick={() => set({ preferredGroups: rules.preferredGroups.filter((other) => other !== group) })}
                className="text-ink-muted hover:text-ink"
              >
                <X aria-hidden className="size-3" />
              </button>
            </span>
          ))}
          <input
            value={typed}
            placeholder="Group name, Enter to add"
            aria-label="Add a preferred release group"
            onChange={(event) => setTyped(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                addGroups();
              }
            }}
            onBlur={addGroups}
            className={`${input} w-52 text-xs`}
          />
        </div>
      </fieldset>
    </div>
  );
}

/** An audio profile's parts: what it prefers, in order, and what it aims for. */
function AudioRulesForm({ rules, onChange }: { rules: AudioRules; onChange: (rules: AudioRules) => void }) {
  const set = (patch: Partial<AudioRules>) => onChange({ ...rules, ...patch });
  const lossyCodecs = Object.keys(CODECS).filter((codec) => !LOSSLESS.has(codec));
  return (
    <div className="flex flex-col gap-4">
      <RankedList
        label="Codecs, best first"
        help="Codecs on one line are equally good. A codec not listed is accepted but ranks last."
        options={CODECS}
        value={rules.codecs}
        onChange={(codecs) => set({ codecs })}
      />
      <Chips
        label="Never accept"
        options={CODECS}
        value={rules.rejectCodecs}
        onChange={(rejectCodecs) => set({ rejectCodecs })}
      />
      <RankedList
        label="File extensions, best first"
        help="Optional: rank by the ending of the file name. Adding one also adds the container it holds below."
        options={EXTENSIONS}
        value={rules.extensions}
        onChange={(extensions) => {
          // Linked: an extension brings its container along, which can be removed again.
          const known = new Set(rules.containers.flat());
          const added = extensions.flat().filter((name) => !rules.extensions.flat().includes(name));
          const linked = added.map(containerOf).filter((name, index, all) => !known.has(name) && all.indexOf(name) === index);
          set({ extensions, containers: linked.length > 0 ? [...rules.containers, ...linked.map((name) => [name])] : rules.containers });
        }}
      />
      <RankedList
        label="Containers, best first"
        help="Optional: rank by the format inside, whatever the file is called."
        options={CONTAINERS}
        value={rules.containers}
        onChange={(containers) => set({ containers })}
      />
      <RankedList label="Channels, best first" options={CHANNELS} value={rules.channels} onChange={(channels) => set({ channels })} />

      <fieldset className="flex flex-col gap-2">
        <legend className="text-xs font-medium">Bitrate targets (kbps)</legend>
        <p className="text-xs text-ink-muted">
          Nearest to the ideal ranks first, above or below. Lossless codecs have no bitrate target.
        </p>
        {rules.bitrates.map((target, index) => (
          <div key={index} className="flex flex-wrap items-end gap-2 text-xs">
            <select
              value={target.codec}
              onChange={(event) =>
                set({ bitrates: rules.bitrates.map((old, i) => (i === index ? { ...old, codec: event.target.value } : old)) })
              }
              className={input}
            >
              <option value="">Any codec</option>
              {lossyCodecs.map((codec) => (
                <option key={codec} value={codec}>
                  {CODECS[codec]}
                </option>
              ))}
            </select>
            <RangeInputs
              value={target}
              onChange={(range) => set({ bitrates: rules.bitrates.map((old, i) => (i === index ? { ...old, ...range } : old)) })}
            />
            <button
              type="button"
              aria-label="Remove this bitrate target"
              onClick={() => set({ bitrates: rules.bitrates.filter((_, i) => i !== index) })}
              className="rounded-md border border-border-subtle p-1 text-ink-muted hover:text-danger"
            >
              <X aria-hidden className="size-3.5" />
            </button>
          </div>
        ))}
        <button
          type="button"
          onClick={() => set({ bitrates: [...rules.bitrates, { codec: "mp3", min: "256", ideal: "320", max: "320" }] })}
          className={`${smallButton} w-fit`}
        >
          Add a bitrate target
        </button>
      </fieldset>

      <OptionalRange
        label="Sample rate target (Hz)"
        value={rules.sampleRate}
        fallback={SAMPLE_RATE_FALLBACK}
        onChange={(sampleRate) => set({ sampleRate })}
      />
      <OptionalRange
        label="Bit depth target"
        value={rules.bitDepth}
        fallback={{ min: "16", ideal: "16", max: "24" }}
        onChange={(bitDepth) => set({ bitDepth })}
      />

      <fieldset className="flex flex-col gap-2">
        <legend className="text-xs font-medium">Preferred languages</legend>
        <p className="text-xs text-ink-muted">
          A preference only breaks a tie between files of equal quality; a file that
          states no language is never refused. The language is read from the file's tags when it is probed.
        </p>
        {(
          [
            ["audio", "Audio language"],
            ["lyrics", "Lyrics language"],
            ["subtitle", "Subtitle language"],
          ] as const
        ).map(([key, label]) => (
          <LanguagePicker
            key={key}
            label={label}
            value={rules.languages[key]}
            onChange={(next) => set({ languages: { ...rules.languages, [key]: next } })}
          />
        ))}
      </fieldset>
    </div>
  );
}

/** An order of preference: each entry below another ranks lower, unless tied with it. */
function RankedList({
  label,
  help,
  options,
  value,
  onChange,
}: {
  label: string;
  help?: string;
  options: Record<string, string>;
  value: string[][];
  onChange: (tiers: string[][]) => void;
}) {
  const items = value.flatMap((tier, tierIndex) => tier.map((item, i) => ({ item, tied: i > 0, tierIndex })));
  const rebuild = (next: { item: string; tied: boolean }[]) => {
    const tiers: string[][] = [];
    for (const entry of next) {
      if (entry.tied && tiers.length > 0) tiers[tiers.length - 1]!.push(entry.item);
      else tiers.push([entry.item]);
    }
    onChange(tiers);
  };
  const flat = items.map(({ item, tied }) => ({ item, tied }));
  const unused = Object.keys(options).filter((option) => !flat.some((entry) => entry.item === option));
  const move = (index: number, by: number) => {
    const next = [...flat];
    const [entry] = next.splice(index, 1);
    next.splice(index + by, 0, entry!);
    rebuild(next);
  };

  return (
    <fieldset className="flex flex-col gap-1.5">
      <legend className="text-xs font-medium">{label}</legend>
      {help && <p className="text-xs text-ink-muted">{help}</p>}
      <ol className="flex flex-col gap-1">
        {flat.map((entry, index) => (
          <li key={entry.item} className="flex items-center gap-2 text-sm">
            <span className="w-6 text-right text-xs text-ink-muted">{entry.tied ? "=" : `${items[index]!.tierIndex + 1}.`}</span>
            <span className="min-w-28">{options[entry.item] ?? entry.item}</span>
            {index > 0 && (
              <label className="flex items-center gap-1 text-xs text-ink-muted">
                <input
                  type="checkbox"
                  checked={entry.tied}
                  onChange={(event) => rebuild(flat.map((old, i) => (i === index ? { ...old, tied: event.target.checked } : old)))}
                />
                as good as the one above
              </label>
            )}
            <span className="ml-auto flex gap-1">
              <IconButton label="Move up" disabled={index === 0} onClick={() => move(index, -1)} icon={ArrowUp} />
              <IconButton label="Move down" disabled={index === flat.length - 1} onClick={() => move(index, 1)} icon={ArrowDown} />
              <IconButton label="Remove" onClick={() => rebuild(flat.filter((_, i) => i !== index))} icon={X} />
            </span>
          </li>
        ))}
      </ol>
      {unused.length > 0 && (
        <select
          value=""
          onChange={(event) => rebuild([...flat, { item: event.target.value, tied: false }])}
          className={`${input} w-fit text-xs`}
          aria-label={`Add to ${label}`}
        >
          <option value="">Add…</option>
          {unused.map((option) => (
            <option key={option} value={option}>
              {options[option]}
            </option>
          ))}
        </select>
      )}
    </fieldset>
  );
}

function Chips({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: Record<string, string>;
  value: string[];
  onChange: (value: string[]) => void;
}) {
  return (
    <fieldset className="flex flex-col gap-1.5">
      <legend className="text-xs font-medium">{label}</legend>
      <div className="flex flex-wrap gap-2 text-xs">
        {Object.entries(options).map(([option, text]) => (
          <label key={option} className="flex items-center gap-1">
            <input
              type="checkbox"
              checked={value.includes(option)}
              onChange={(event) => onChange(event.target.checked ? [...value, option] : value.filter((item) => item !== option))}
            />
            {text}
          </label>
        ))}
      </div>
    </fieldset>
  );
}

function RangeInputs({ value, onChange }: { value: Range; onChange: (range: Range) => void }) {
  return (
    <>
      {(["min", "ideal", "max"] as const).map((key) => (
        <label key={key} className={labelStyle}>
          {key === "min" ? "Lowest" : key === "ideal" ? "Ideal" : "Highest"}
          <input
            inputMode="numeric"
            required={key === "ideal"}
            value={value[key]}
            onChange={(event) => onChange({ ...value, [key]: event.target.value.replace(/[^\d.]/g, "") })}
            className={`${input} w-24`}
          />
        </label>
      ))}
    </>
  );
}

function OptionalRange({
  label,
  value,
  fallback,
  onChange,
}: {
  label: string;
  value: Range | null;
  fallback: Range;
  onChange: (range: Range | null) => void;
}) {
  return (
    <fieldset className="flex flex-col gap-1.5">
      <label className="flex items-center gap-2 text-xs font-medium">
        <input type="checkbox" checked={value !== null} onChange={(event) => onChange(event.target.checked ? fallback : null)} />
        {label}
      </label>
      {value && (
        <div className="flex flex-wrap items-end gap-2">
          <RangeInputs value={value} onChange={onChange} />
        </div>
      )}
    </fieldset>
  );
}

function IconButton({
  label,
  icon: Icon,
  disabled,
  onClick,
}: {
  label: string;
  icon: typeof ArrowUp;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      disabled={disabled}
      onClick={onClick}
      className="rounded-md border border-border-subtle p-1 text-ink-muted hover:text-ink disabled:opacity-40"
    >
      <Icon aria-hidden className="size-3.5" />
    </button>
  );
}

const input = "rounded-md border border-border-subtle bg-canvas px-2 py-1 text-sm text-ink";
const smallButton = "rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted hover:text-ink";
