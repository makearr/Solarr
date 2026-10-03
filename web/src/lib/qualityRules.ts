/** The parts of a quality profile's rules document the profile form edits, and the round trip
 *  between them. Everything the form does not show is kept as it was (ADR-40's full shape stays
 *  editable as JSON at the expert level). */

export interface Range {
  min: string;
  ideal: string;
  max: string;
}

export interface BitrateTarget extends Range {
  /** The codec the target is for; empty applies to every codec. */
  codec: string;
}

/** Languages a profile prefers, as two-letter codes: a tie-breaker on the measured file, never a gate (M1g 1.7). */
export interface LanguagePreferences {
  audio: string[];
  lyrics: string[];
  subtitle: string[];
}

export interface AudioRules {
  codecs: string[][];
  /** File name endings and the container formats they hold: two dimensions, linked (M1g 1.7). */
  extensions: string[][];
  containers: string[][];
  channels: string[][];
  rejectCodecs: string[];
  bitrates: BitrateTarget[];
  sampleRate: Range | null;
  bitDepth: Range | null;
  languages: LanguagePreferences;
}

type Target = { when?: Record<string, string>; min?: string; ideal: string; max?: string; weights?: unknown };
type ScoringRule = { name: string; score: number; specs: { field: string; has?: string; eq?: string; required?: boolean }[] };
type Document = {
  scoring?: ScoringRule[];
  quality_profile?: string;
  require?: { reject_codecs?: string[]; [key: string]: unknown };
  compare?: string[];
  tiers?: Record<string, string[][]>;
  targets?: Record<string, Target[]>;
  [key: string]: unknown;
};

/** The compare entry each part of the form owns, in the order new ones are added. */
const OWNED: readonly [keyof AudioRules, string][] = [
  ["codecs", "audio_codec"],
  ["extensions", "extension"],
  ["containers", "container"],
  ["channels", "audio_channels"],
  ["bitrates", "bitrate_distance"],
  ["sampleRate", "sample_rate_distance"],
  ["bitDepth", "bit_depth_distance"],
];

/** The container format a file extension (or an older profile's "m4a") stands for; the server's own table. */
export const CONTAINER_OF: Record<string, string> = {
  m4a: "mp4", m4b: "mp4", mp4: "mp4", mp3: "mp3", flac: "flac", wav: "wav", ogg: "ogg", oga: "ogg",
  opus: "ogg", wma: "asf", asf: "asf", aac: "aac", ape: "ape", mka: "matroska", mkv: "matroska",
};
export const containerOf = (name: string) => CONTAINER_OF[name.toLowerCase().replace(/^\./, "")] ?? name.toLowerCase();

// Tiers naming the same container twice (an older profile's "m4a" and "mp4") collapse to one entry.
function canonicalTiers(tiers: string[][]): string[][] {
  const seen = new Set<string>();
  return tiers
    .map((tier) => tier.map(containerOf).filter((name) => !seen.has(name) && seen.add(name)))
    .filter((tier) => tier.length > 0);
}

/** A preferred language is one scoring rule named by its kind and code, which is how it is told from the profile's own rules. */
const LANGUAGE_FIELDS: readonly [keyof LanguagePreferences, string, string][] = [
  ["audio", "audio_languages", "Preferred audio language: "],
  ["lyrics", "lyrics_languages", "Preferred lyrics language: "],
  ["subtitle", "subtitle_languages", "Preferred subtitle language: "],
];
const LANGUAGE_SCORE = 2;

function parseLanguages(doc: Document): LanguagePreferences {
  const languages: LanguagePreferences = { audio: [], lyrics: [], subtitle: [] };
  for (const [key, , prefix] of LANGUAGE_FIELDS) {
    for (const rule of doc.scoring ?? []) {
      if (rule.name.startsWith(prefix)) languages[key].push(rule.name.slice(prefix.length));
    }
  }
  return languages;
}

const KBPS = /^(\d+(?:\.\d+)?)\s*kbps$/i;

/** The kbps number of "320kbps"; the value unchanged when it has another unit. */
export function kbps(value: string | undefined): string {
  const match = KBPS.exec(value ?? "");
  return match ? match[1]! : (value ?? "");
}

function range(target: Target | undefined): Range | null {
  if (!target) return null;
  return { min: target.min ?? "", ideal: target.ideal, max: target.max ?? "" };
}

export function parseAudioRules(json: string): AudioRules {
  const doc = JSON.parse(json || "{}") as Document;
  return {
    codecs: doc.tiers?.audio_codec ?? [],
    extensions: doc.tiers?.extension ?? [],
    containers: canonicalTiers(doc.tiers?.container ?? []),
    channels: doc.tiers?.audio_channels ?? [],
    rejectCodecs: doc.require?.reject_codecs ?? [],
    bitrates: (doc.targets?.bitrate ?? []).map((target) => ({
      codec: target.when?.audio_codec ?? "",
      min: kbps(target.min),
      ideal: kbps(target.ideal),
      max: kbps(target.max),
    })),
    sampleRate: range(doc.targets?.sample_rate?.[0]),
    bitDepth: range(doc.targets?.bit_depth?.[0]),
    languages: parseLanguages(doc),
  };
}

function isSet(rules: AudioRules, key: keyof AudioRules): boolean {
  const value = rules[key];
  return Array.isArray(value) ? value.length > 0 : value !== null;
}

function toTarget(values: Range, unit: string, previous?: Target): Target {
  const target: Target = { ideal: values.ideal + unit };
  if (values.min) target.min = values.min + unit;
  if (values.max) target.max = values.max + unit;
  if (previous?.weights) target.weights = previous.weights;
  return target;
}

/** The rules document with the form's parts replaced and everything else as it was. */
export function buildAudioRules(json: string, name: string, rules: AudioRules): string {
  const doc = JSON.parse(json || "{}") as Document;
  doc.quality_profile = name;

  const tiers = { ...(doc.tiers ?? {}) };
  for (const [key, dimension] of [["codecs", "audio_codec"], ["extensions", "extension"], ["containers", "container"], ["channels", "audio_channels"]] as const) {
    if (rules[key].length > 0) tiers[dimension] = rules[key];
    else delete tiers[dimension];
  }
  setOrDrop(doc, "tiers", tiers);

  const targets = { ...(doc.targets ?? {}) };
  const oldBitrates = targets.bitrate ?? [];
  if (rules.bitrates.length > 0) {
    targets.bitrate = rules.bitrates.map((target) => {
      const previous = oldBitrates.find((old) => (old.when?.audio_codec ?? "") === target.codec);
      const built = toTarget(target, "kbps", previous);
      if (target.codec) built.when = { audio_codec: target.codec };
      return built;
    });
  } else delete targets.bitrate;
  for (const [key, dimension] of [["sampleRate", "sample_rate"], ["bitDepth", "bit_depth"]] as const) {
    const value = rules[key];
    if (value) targets[dimension] = [toTarget(value, "", targets[dimension]?.[0])];
    else delete targets[dimension];
  }
  setOrDrop(doc, "targets", targets);

  const require = { ...(doc.require ?? {}) };
  if (rules.rejectCodecs.length > 0) require.reject_codecs = rules.rejectCodecs;
  else delete require.reject_codecs;
  setOrDrop(doc, "require", require);

  // Owned entries keep their place; new ones go before the score, which always comes last.
  let compare = (doc.compare ?? []).filter((entry) => {
    const owner = OWNED.find(([, owned]) => owned === entry);
    return !owner || isSet(rules, owner[0]);
  });
  for (const [key, entry] of OWNED) {
    if (isSet(rules, key) && !compare.includes(entry)) {
      compare = compare.filter((existing) => existing !== "score").concat(entry);
    }
  }
  doc.compare = compare.filter((entry) => entry !== "score").concat("score");

  const ownRule = (rule: ScoringRule) => LANGUAGE_FIELDS.some(([, , prefix]) => rule.name.startsWith(prefix));
  const scoring = (doc.scoring ?? []).filter((rule) => !ownRule(rule));
  for (const [key, field, prefix] of LANGUAGE_FIELDS) {
    for (const code of rules.languages[key]) {
      scoring.push({ name: prefix + code, score: LANGUAGE_SCORE, specs: [{ field, has: code, required: true }] });
    }
  }
  if (scoring.length > 0) doc.scoring = scoring;
  else delete doc.scoring;

  return JSON.stringify(doc);
}

// An empty section is left out, as the shipped profiles leave out what they do not use.
function setOrDrop(doc: Document, key: "tiers" | "targets" | "require", value: object) {
  if (Object.keys(value).length > 0) (doc as Record<string, unknown>)[key] = value;
  else delete doc[key];
}

/** A one-line summary, as a profile list shows it: "flac/alac > opus > aac > mp3", or a video profile's resolutions. */
export function summarise(json: string): string {
  try {
    const rules = parseAudioRules(json);
    const resolutions = (JSON.parse(json || "{}") as Document).tiers?.resolution ?? [];
    const audio = rules.codecs.length > 0 ? rules.codecs : rules.containers;
    const tiers = resolutions.length > 0 ? resolutions : audio;
    return tiers.map((tier) => tier.join("/")).join(" > ");
  } catch {
    return "";
  }
}

/** What the form edits of a video profile (TV): the tiers in the order they rank, the resolution gate, the audio
 *  language gate and the release groups that score higher. Everything else in the document is kept as it was. */
export interface VideoRules {
  resolutions: string[][];
  sources: string[][];
  videoCodecs: string[][];
  hdr: string[][];
  audioCodecs: string[][];
  audioChannels: string[][];
  rejectCodecs: string[];
  /** The lowest and highest resolution accepted; empty means no limit on that side. */
  minResolution: string;
  maxResolution: string;
  /** Audio languages of which at least one must be present; empty means no gate. */
  audioLanguages: string[];
  preferredGroups: string[];
}

const VIDEO_TIERS: readonly [keyof VideoRules, string][] = [
  ["resolutions", "resolution"],
  ["sources", "source"],
  ["videoCodecs", "video_codec"],
  ["hdr", "hdr"],
  ["audioChannels", "audio_channels"],
  ["audioCodecs", "audio_codec"],
];

const GROUP_PREFIX = "Preferred release group: ";
/** Heard as a tie-breaker between releases of equal quality, as the language preferences are. */
const GROUP_SCORE = 5;

type VideoDocument = Document & { require?: { resolution?: { min?: string; max?: string }; audio_language?: { any_of?: string[] } } };

export function parseVideoRules(json: string): VideoRules {
  const doc = JSON.parse(json || "{}") as VideoDocument;
  return {
    resolutions: doc.tiers?.resolution ?? [],
    sources: doc.tiers?.source ?? [],
    videoCodecs: doc.tiers?.video_codec ?? [],
    hdr: doc.tiers?.hdr ?? [],
    audioCodecs: doc.tiers?.audio_codec ?? [],
    audioChannels: doc.tiers?.audio_channels ?? [],
    rejectCodecs: doc.require?.reject_codecs ?? [],
    minResolution: doc.require?.resolution?.min ?? "",
    maxResolution: doc.require?.resolution?.max ?? "",
    audioLanguages: doc.require?.audio_language?.any_of ?? [],
    preferredGroups: (doc.scoring ?? []).filter((rule) => rule.name.startsWith(GROUP_PREFIX)).map((rule) => rule.name.slice(GROUP_PREFIX.length)),
  };
}

/** The rules document with the form's parts replaced and everything else (targets, other scoring rules) as it was. */
export function buildVideoRules(json: string, name: string, rules: VideoRules): string {
  const doc = JSON.parse(json || "{}") as VideoDocument;
  doc.quality_profile = name;

  const tiers = { ...(doc.tiers ?? {}) };
  for (const [key, dimension] of VIDEO_TIERS) {
    const value = rules[key] as string[][];
    if (value.length > 0) tiers[dimension] = value;
    else delete tiers[dimension];
  }
  setOrDrop(doc, "tiers", tiers);

  const require = { ...(doc.require ?? {}) };
  if (rules.rejectCodecs.length > 0) require.reject_codecs = rules.rejectCodecs;
  else delete require.reject_codecs;
  if (rules.minResolution && rules.maxResolution) require.resolution = { min: rules.minResolution, max: rules.maxResolution };
  else delete require.resolution;
  if (rules.audioLanguages.length > 0) require.audio_language = { any_of: rules.audioLanguages };
  else delete require.audio_language;
  setOrDrop(doc, "require", require);

  let compare = (doc.compare ?? []).filter((entry) => {
    const owner = VIDEO_TIERS.find(([, owned]) => owned === entry);
    return !owner || (rules[owner[0]] as string[][]).length > 0;
  });
  for (const [key, entry] of VIDEO_TIERS) {
    if ((rules[key] as string[][]).length > 0 && !compare.includes(entry)) {
      compare = compare.filter((existing) => existing !== "score").concat(entry);
    }
  }
  doc.compare = compare.filter((entry) => entry !== "score").concat("score");

  const scoring = (doc.scoring ?? []).filter((rule) => !rule.name.startsWith(GROUP_PREFIX));
  for (const group of rules.preferredGroups) {
    scoring.push({ name: GROUP_PREFIX + group, score: GROUP_SCORE, specs: [{ field: "release_group", eq: group, required: true }] });
  }
  if (scoring.length > 0) doc.scoring = scoring;
  else delete doc.scoring;

  return JSON.stringify(doc);
}
