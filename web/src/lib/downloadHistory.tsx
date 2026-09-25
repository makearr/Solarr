import type { components } from "@/api/schema";

export type HistoryEntry = components["schemas"]["HistoryEntry"];

const KIND_LABEL: Record<HistoryEntry["kind"], string> = {
  grabbed: "Grabbed",
  imported: "Imported",
  failed: "Failed",
};

const KIND_TONE: Record<HistoryEntry["kind"], string> = {
  grabbed: "text-accent",
  imported: "text-ok",
  failed: "text-danger",
};

/** What happened to a download, in the words Lidarr's history uses. */
export function HistoryKind({ kind }: { kind: HistoryEntry["kind"] }) {
  return <span className={KIND_TONE[kind]}>{KIND_LABEL[kind]}</span>;
}

/** Where the release came from and went to: "NZBgeek → SABnzbd". */
export function historySource(entry: HistoryEntry): string {
  return [entry.indexer, entry.client].filter(Boolean).join(" → ");
}

export function historyWhen(entry: HistoryEntry): string {
  return new Date(entry.at).toLocaleString(undefined, { hour12: false });
}
