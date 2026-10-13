import type { components } from "@/api/schema";

type MediaTypeInfo = components["schemas"]["MediaTypeInfo"];

// One English catalogue: every label of the shared pages, with {placeholders} filled from the media type's words.
const catalogue = {
  "action.monitor": "Monitor",
  "action.unmonitor": "Unmonitor",
  "action.search": "Search",
  "action.interactive": "Interactive search",
  "action.refresh": "Refresh",
  "action.edit": "Edit",
  "action.renamePreview": "Rename preview",
  "action.remove": "Remove",
  "action.cancel": "Cancel",
  "action.close": "Close",
  "action.searchAll": "Search all {works}",
  "tab.overview": "Overview",
  "tab.files": "Files",
  "tab.history": "History",
  "tab.search": "Search",
  "work.notFound": "This {work} could not be found.",
  "level.notFound": "This library has no such list.",
  "work.loading": "Loading…",
  "work.partsOnDisk": "{present} of {required} {parts} on disk",
  "work.partsPresent": "{present} {parts} on disk",
  "work.outBasis": "Known from: {basis}. Counted as missing {hours} hours after.",
  "work.outUnannounced": "No date announced yet.",
  "work.waitingForMetadata": "Waiting for metadata",
  "work.noParts": "No {parts} are known yet.",
  "work.notWanted": "Nothing of this {work} is wanted.",
  "work.remove.title": "Remove {title}",
  "work.remove.consequence": "The {work} leaves the library. Its files stay on disk unless you also delete them.",
  "work.remove.deleteFiles": "Also delete the files (to the recycle bin)",
  "search.none": "Nothing searched yet.",
  "search.failed": "The search could not run: is an indexer connected and enabled?",
  "grab.sent": "Sent to the download client",
  "grab.alreadyDownloading": "Already downloading",
  "grab.held": "Held for room in the client's queue",
  "grab.nothingFound": "Nothing found",
  "grab.noneSendable": "Found releases, none could be sent",
} as const;

export type MessageKey = keyof typeof catalogue;

/** The words of a media type a label may name: {work}, {Work}, {works}, {Works}, and the same for part and group. */
export type Vocabulary = Record<string, string>;

export function vocabularyOf(info: MediaTypeInfo | undefined): Vocabulary {
  const words: Vocabulary = {};
  const add = (name: string, one: string | undefined, many: string | undefined, fallbackOne: string, fallbackMany: string) => {
    const singular = one || fallbackOne;
    const plural = many || fallbackMany;
    words[name] = singular.toLowerCase();
    words[name.charAt(0).toUpperCase() + name.slice(1)] = singular;
    words[`${name}s`] = plural.toLowerCase();
    words[`${name.charAt(0).toUpperCase() + name.slice(1)}s`] = plural;
  };
  add("work", info?.work.one, info?.work.many, "Item", "Items");
  add("part", info?.part.one, info?.part.many, "Part", "Parts");
  add("group", info?.group.one, info?.group.many, "Group", "Groups");
  return words;
}

/** The catalogue's text for key, its {placeholders} filled from values; an unknown placeholder stays as written. */
export function t(key: MessageKey, values: Record<string, string | number> = {}): string {
  return catalogue[key].replace(/\{(\w+)\}/g, (whole, name: string) => (name in values ? String(values[name]) : whole));
}
