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

export interface AudioRules {
  codecs: string[][];
  containers: string[][];
  channels: string[][];
  rejectCodecs: string[];
  bitrates: BitrateTarget[];
  sampleRate: Range | null;
  bitDepth: Range | null;
}

type Target = { when?: Record<string, string>; min?: string; ideal: string; max?: string; weights?: unknown };
type Document = {
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
  ["containers", "container"],
  ["channels", "audio_channels"],
  ["bitrates", "bitrate_distance"],
  ["sampleRate", "sample_rate_distance"],
  ["bitDepth", "bit_depth_distance"],
];

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
    containers: doc.tiers?.container ?? [],
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
  for (const [key, dimension] of [["codecs", "audio_codec"], ["containers", "container"], ["channels", "audio_channels"]] as const) {
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

  return JSON.stringify(doc);
}

// An empty section is left out, as the shipped profiles leave out what they do not use.
function setOrDrop(doc: Document, key: "tiers" | "targets" | "require", value: object) {
  if (Object.keys(value).length > 0) (doc as Record<string, unknown>)[key] = value;
  else delete doc[key];
}

/** A one-line summary, as a profile list shows it: "flac/alac > opus > aac > mp3". */
export function summarise(json: string): string {
  try {
    const rules = parseAudioRules(json);
    const tiers = rules.codecs.length > 0 ? rules.codecs : rules.containers;
    return tiers.map((tier) => tier.join("/")).join(" > ");
  } catch {
    return "";
  }
}
