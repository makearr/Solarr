/** Plain-language reading of an identification run's structured evidence
 *  (documentation/music-identification.md), shared by every screen that asks
 *  the user to decide. */

export interface EvidenceFact {
  kind: string;
  detail: string;
}

/** The candidate's facts by kind; a kind appearing twice keeps the first. */
export function factsOf(evidence: readonly EvidenceFact[] | undefined): Record<string, string> {
  const facts: Record<string, string> = {};
  for (const fact of evidence ?? []) {
    if (!(fact.kind in facts)) facts[fact.kind] = fact.detail;
  }
  return facts;
}

const PASS_LABELS: Record<string, string> = {
  embedded_tags: "MusicBrainz IDs in the file",
  album_match: "Whole album against MusicBrainz tracklists",
  text_search: "Title search per file",
  fingerprint: "Audio fingerprint (AcoustID)",
  track_match: "The track on its own, then the album it comes from",
  folder_siblings: "Other files in the same folder",
};

export function passLabel(pass: string): string {
  return PASS_LABELS[pass] ?? pass;
}

const OUTCOME_LABELS: Record<string, string> = {
  identified: "Identified",
  none: "Nothing found",
  failed: "Lookup failed -- retried on the next scan",
  no_candidate: "No album found on MusicBrainz",
  insufficient_evidence: "Albums found, none fits the files well enough",
  ambiguous: "Several albums fit about equally well -- your choice",
  contradictory: "Track lengths contradict the album the tags name",
  joined: "Joined the album the other files agreed on",
  siblings_disagree: "The other files point at different albums",
  no_identified_sibling: "No other file in the folder was identified",
  retired: "The album ID in the tags no longer exists at MusicBrainz",
  unplaced: "The album fits the folder, but has no track for this file",
  disabled_no_key: "Skipped: no AcoustID key is set (Settings -> Metadata)",
};

export function outcomeLabel(outcome: string): string {
  return OUTCOME_LABELS[outcome] ?? outcome;
}

const ATTEMPT_LABELS: Record<string, string> = {
  identified: "Identified",
  queued: "Needs your decision",
  retry: "Interrupted -- will be retried",
};

export function attemptLabel(outcome: string): string {
  return ATTEMPT_LABELS[outcome] ?? outcome;
}

/** How one album candidate fared, from its accepted/contradicted facts. */
export function verdictOf(facts: Record<string, string>): string {
  if (facts.contradicted === "true") return "Track lengths or recordings differ";
  if (facts.accepted === "true") return "Fits";
  if (facts.accepted === "false") return "Does not fit well enough";
  return "";
}

/** "Kebilegev Bibihi · 2017-10-13 · US · official · 13 tracks", skipping what is unknown. */
export function editionSummary(facts: Record<string, string>): string {
  const parts = [facts.edition_title, facts.edition_date, facts.edition_country, facts.edition_status];
  if (facts.edition_tracks) parts.push(`${facts.edition_tracks} tracks`);
  return parts.filter((part) => part !== undefined && part !== "").join(" · ");
}

const RECALL_LABELS: Record<string, string> = {
  sibling: "Other files' album",
  album_tags: "Album tag search",
  recording_search: "Track title search",
};

export function recallLabel(recall: string | undefined): string {
  return recall === undefined ? "" : (RECALL_LABELS[recall] ?? recall);
}
