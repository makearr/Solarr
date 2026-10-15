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
  "work.notOutUntil": "Not out yet · {date}",
  "work.noParts": "No {parts} are known yet.",
  "work.notWanted": "Nothing of this {work} is wanted.",
  "work.remove.title": "Remove {title}",
  "work.remove.consequence": "The {work} leaves the library. Its files stay on disk unless you also delete them.",
  "work.remove.deleteFiles": "Also delete the files (to the recycle bin)",
  "work.merge.heldTwice": "This {work} is held twice:",
  "work.merge.sameRelease": "is the same release.",
  "work.merge.review": "Merge into it…",
  "work.merge.preview": "Its {files} files, what is wanted of it and its {downloads} downloads move onto {other}; this {work} is removed and its name kept as the other's alias.",
  "work.merge.confirm": "Merge",
  "work.merge.failed": "The merge was refused.",
  "numbering.title": "Numbering",
  "numbering.by": "Number the episodes by",
  "numbering.preview": "Preview",
  "numbering.apply": "Apply",
  "numbering.applied": "Renumbered. The files keep their names until you rename them (Rename preview).",
  "numbering.failed": "That could not be done.",
  "numbering.none": "This has no other numbering.",
  "numbering.summary": "{moves} move, {unchanged} stay where they are, {new} places are new.",
  "numbering.hasFile": "has a file",
  "numbering.setAside": "{count} not in this list are set aside, their files kept: {list}.",
  "numbering.renameAfter": "Files keep their names until renamed; Rename preview shows the new ones afterwards.",
  "crossover.title": "Crossovers",
  "crossover.help": "Events across series in watch order, and episodes that are one broadcast listed under two series.",
  "crossover.none": "No crossover events yet. Add an episode to one from its series page.",
  "crossover.failed": "That could not be done.",
  "crossover.suggestions": "Possibly one broadcast in two series",
  "crossover.suggestionsHelp": "Same title, same day. Linked, one file holds both; nothing is linked until you say so.",
  "crossover.sameBroadcast": "Same broadcast",
  "crossover.onDisk": "on disk",
  "crossover.missing": "missing",
  "crossover.unmonitored": "not monitored",
  "crossover.removeMember": "Remove from the event",
  "crossover.parts": "{count} episodes in watch order",
  "crossover.monitorAll": "Monitor the whole event",
  "crossover.unmonitorAll": "Unmonitor the event",
  "crossover.delete": "Delete the event",
  "crossover.badge": "{name}, {at} of {of}",
  "crossover.also": "also {series} {ref}",
  "crossover.action": "Crossover…",
  "crossover.joinEvent": "Part of an event",
  "crossover.newEvent": "A new event…",
  "crossover.eventName": "Event name",
  "crossover.add": "Add",
  "crossover.joinHelp": "It joins at the end; reorder on the event's page by removing and adding.",
  "crossover.sameAs": "The same broadcast as an episode of another series",
  "crossover.findSeries": "Find the other series",
  "crossover.series": "Series…",
  "crossover.episode": "Episode…",
  "crossover.sameHelp": "One file then holds both: the episode counts as on disk in both series.",
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
