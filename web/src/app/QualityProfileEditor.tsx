import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Plus, Trash2, X } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { mediaTypeName } from "@/lib/mediaTypeIcon";
import { buildAudioRules, containerOf, parseAudioRules, summarise, type AudioRules, type Range } from "@/lib/qualityRules";
import { isTierVisible, useTier } from "@/lib/useTier";

import { LanguagePicker } from "./LanguagePicker";

type QualityProfile = components["schemas"]["QualityProfile"];

// Every media type Solarr plans (settings.MediaTypes); the audio ones have a form.
const MEDIA_TYPES = ["music", "audiobook", "podcast", "tv", "movie", "youtube", "news", "book", "comic", "rom", "adult"];
const AUDIO = new Set(["music", "audiobook", "podcast"]);

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
  const profiles = useQuery({
    queryKey: ["qualityProfiles"],
    queryFn: async () => {
      const { data, error } = await api.GET("/qualityprofiles");
      if (error) throw error;
      return data;
    },
  });
  const remove = useMutation({
    mutationFn: async (id: number) => {
      const { error } = await api.DELETE("/qualityprofiles/{id}", { params: { path: { id } } });
      if (error) throw error;
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
      <div className="flex items-center gap-3">
        <h1 className="text-lg font-semibold">Quality profiles</h1>
        <button
          type="button"
          onClick={() => setEditing("new")}
          className="ml-auto flex items-center gap-1 rounded-md border border-border-subtle px-3 py-1.5 text-sm
                     text-ink-muted hover:text-ink"
        >
          <Plus aria-hidden className="size-4" />
          New profile
        </button>
      </div>
      <p className="text-sm text-ink-muted">
        What each kind of file is worth. A library lists the profiles it accepts, in order, under its own
        Quality page; the same profile can serve several libraries.
      </p>
      {remove.isError && (
        <p className="text-xs text-danger">
          {(remove.error as { detail?: string }).detail ?? "That profile could not be removed."}
        </p>
      )}
      {editing === "new" && <ProfileForm onDone={done} onCancel={() => setEditing(null)} />}
      {profiles.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}

      {groups.map((group) => (
        <section key={group.mediaType} className="flex flex-col gap-2">
          <h2 className="text-sm font-medium">{mediaTypeName(group.mediaType)}</h2>
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
  // A new profile starts with the sample rate target on (asked 2026-09-30); an existing one keeps its own.
  const [rules, setRules] = useState<AudioRules>(() =>
    initial ? parseAudioRules(initial.rules) : { ...parseAudioRules(""), sampleRate: SAMPLE_RATE_FALLBACK },
  );
  const [json, setJson] = useState(initial?.rules ?? "");
  const [asJSON, setAsJSON] = useState(false);
  const [upgrade, setUpgrade] = useState(initial?.upgrade ?? DEFAULT_UPGRADE);
  const hasForm = AUDIO.has(mediaType);

  const save = useMutation({
    mutationFn: async () => {
      const body = {
        name,
        rules: hasForm && !asJSON ? buildAudioRules(initial?.rules ?? "", name, rules) : json,
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
        <label className="flex flex-col gap-1 text-xs text-ink-muted">
          Media type
          <select
            value={mediaType}
            disabled={initial !== undefined}
            onChange={(event) => setMediaType(event.target.value)}
            className={input}
          >
            {MEDIA_TYPES.map((type) => (
              <option key={type} value={type}>
                {mediaTypeName(type)}
              </option>
            ))}
          </select>
        </label>
      </div>

      {hasForm && !asJSON && <AudioRulesForm rules={rules} onChange={setRules} />}
      {(!hasForm || asJSON) && (
        <label className="flex flex-col gap-1 text-xs text-ink-muted">
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
                    if (event.target.checked) setJson(buildAudioRules(initial?.rules ?? "", name, rules));
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
        <label key={key} className="flex flex-col gap-1 text-xs text-ink-muted">
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
