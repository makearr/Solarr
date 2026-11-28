import { isTierVisible, type Tier } from "@/lib/useTier";

/** One page of a section, shown as a tab above the page. */
export interface SectionTab {
  readonly label: string;
  readonly path: string;
  /** Query the page needs to show this tab's content; a tab is active only when it matches. */
  readonly search?: Readonly<Record<string, string>>;
  readonly tier: Tier;
  /** Only an administrator may open it. */
  readonly administrator?: boolean;
  /** The Settings sub-group the page sits in. */
  readonly group?: SettingsGroup;
}

/** A sidebar entry and the tabs behind it. */
export interface Section {
  /** Stable name, also what the last-opened tab is remembered under. */
  readonly key: string;
  readonly label: string;
  readonly tabs: readonly SectionTab[];
}

export const ACTIVITY: Section = {
  key: "activity",
  label: "Activity",
  tabs: [
    { label: "Queue", path: "/activity/queue", tier: "normal" },
    { label: "History", path: "/activity/history", tier: "normal" },
    { label: "Requests", path: "/activity/requests", tier: "normal" },
    { label: "Notifications", path: "/activity/notifications", tier: "normal" },
    { label: "Jobs", path: "/activity/jobs", tier: "advanced" },
  ],
};

/** Settings' four sub-groups in sidebar order; a tab names its group. */
/** Everything Solarr talks to, one sidebar entry of its own: where downloads come from and where the library is shown. */
export const CONNECTIONS: Section = {
  key: "connections",
  label: "Connections",
  tabs: [
    { label: "Download clients", path: "/connections/downloaders", tier: "normal", administrator: true },
    { label: "Indexers", path: "/connections/indexers", tier: "normal", administrator: true },
    { label: "Arr apps", path: "/connections/arr", tier: "normal", administrator: true },
    { label: "Media servers", path: "/connections/media-servers", tier: "normal", administrator: true },
    { label: "Import lists", path: "/connections/import-lists", tier: "advanced", administrator: true },
    { label: "Webhooks", path: "/connections/webhooks", tier: "advanced", administrator: true },
  ],
};

export const SETTINGS_GROUPS = ["Media", "Storage", "System"] as const;
export type SettingsGroup = (typeof SETTINGS_GROUPS)[number];

export const SETTINGS: Section = {
  key: "settings",
  label: "Settings",
  tabs: [
    { group: "Media", label: "Libraries", path: "/settings/libraries", tier: "normal", administrator: true },
    { group: "Media", label: "Quality", path: "/quality-profiles", tier: "expert", administrator: true },
    { group: "Media", label: "Metadata", path: "/settings/metadata", tier: "expert", administrator: true },
    { group: "Media", label: "Discover", path: "/settings/discover", tier: "normal", administrator: true },
    { group: "Media", label: "Transcode", path: "/settings/transcode", tier: "expert", administrator: true },
    { group: "Media", label: "Player", path: "/settings/player", tier: "normal", administrator: true },
    { group: "Media", label: "Tags", path: "/settings/tags", tier: "normal", administrator: true },
    { group: "Media", label: "Blocklists", path: "/settings/blocklists", tier: "expert", administrator: true },
    { group: "Storage", label: "Folders", path: "/storage/folders", tier: "advanced", administrator: true },
    { group: "Storage", label: "Import", path: "/storage/import", tier: "advanced", administrator: true },
    { group: "Storage", label: "Duplicates", path: "/storage/duplicates", tier: "advanced", administrator: true },
    { group: "Storage", label: "Rename", path: "/storage/rename", tier: "advanced", administrator: true },
    { group: "Storage", label: "Recycle bin", path: "/storage/recycle-bin", tier: "advanced", administrator: true },
    // Every library's waiting files at once; one library's are under its own Files.
    { group: "Storage", label: "Unidentified", path: "/unidentified", tier: "advanced" },
    { group: "System", label: "General", path: "/settings/general", tier: "normal", administrator: true },
    { group: "System", label: "Network", path: "/settings/network", tier: "advanced", administrator: true },
    { group: "System", label: "Scheduler", path: "/settings/scheduler", tier: "normal", administrator: true },
    { group: "System", label: "Backups", path: "/settings/backups", tier: "normal", administrator: true },
    { group: "System", label: "Cache", path: "/settings/cache", tier: "expert", administrator: true },
    { group: "System", label: "Disk", path: "/settings/disk", tier: "expert", administrator: true },
    { group: "System", label: "Database", path: "/settings/database", tier: "expert", administrator: true },
    { group: "System", label: "Logs", path: "/settings/logs", tier: "advanced", administrator: true },
    { group: "System", label: "Providers", path: "/settings/providers", tier: "advanced", administrator: true },
  ],
};

export const GLOBAL_SECTIONS: readonly Section[] = [ACTIVITY, CONNECTIONS, SETTINGS];

/** A library's four entries after its own levels: what it lacks, what happened and how it stands, its files, its
 *  configuration. */
export function librarySections(categoryId: string, kinds: readonly LibraryKind[] = []): readonly Section[] {
  if (kinds.length < 2) return ownSections(categoryId);
  // A mixed library's entries hold each of its types' pages, named by type (plan 10-libraries L4).
  return ownSections(categoryId).map((section, index) => ({
    ...section,
    tabs: kinds.flatMap((kind) =>
      ownSections(kind.categoryId)[index]!.tabs.map((tab) => ({ ...tab, label: `${tab.label} · ${kind.label}` })),
    ),
  }));
}

/** One media type of a library: its category and what its works are called ("Movies"). */
export interface LibraryKind {
  readonly categoryId: string;
  readonly label: string;
}

function ownSections(categoryId: string): readonly Section[] {
  const base = `/library/${categoryId}`;
  return [
    {
      key: `library:${categoryId}:wanted`,
      label: "Wanted",
      tabs: [{ label: "Wanted", path: `${base}/wanted`, tier: "normal" }],
    },
    {
      key: `library:${categoryId}:insights`,
      label: "Insights",
      tabs: [
        { label: "Statistics", path: `${base}/statistics`, tier: "normal" },
        { label: "Activity", path: `${base}/activity`, tier: "advanced" },
      ],
    },
    {
      key: `library:${categoryId}:files`,
      label: "Files",
      tabs: [
        { label: "Unidentified", path: "/unidentified", search: { category_id: categoryId }, tier: "normal" },
        { label: "Import", path: `${base}/import`, tier: "advanced", administrator: true },
        { label: "Duplicates", path: `${base}/duplicates`, tier: "advanced" },
        { label: "Defective", path: `${base}/defects`, tier: "normal" },
        { label: "Packs", path: `${base}/packs`, tier: "expert", administrator: true },
        { label: "Belongs elsewhere", path: `${base}/check`, tier: "normal" },
      ],
    },
    {
      key: `library:${categoryId}:settings`,
      label: "Settings",
      tabs: [
        { label: "Quality", path: `${base}/config/quality`, tier: "expert", administrator: true },
        { label: "Naming", path: `${base}/config/naming`, tier: "advanced", administrator: true },
        { label: "Metadata", path: `${base}/config/metadata`, tier: "expert", administrator: true },
        { label: "File management", path: `${base}/config/file-management`, tier: "expert", administrator: true },
        { label: "Import lists", path: `${base}/config/import-lists`, tier: "advanced", administrator: true },
        { label: "Suggested for", path: `${base}/config/rules`, tier: "advanced", administrator: true },
      ],
    },
    {
      key: `library:${categoryId}:watched`,
      label: "Watched",
      tabs: [{ label: "Watched", path: `${base}/watched`, tier: "advanced", administrator: true }],
    },
  ];
}

/** Who is looking: a tab they may not open is absent, never disabled. */
export interface Viewer {
  readonly tier: Tier;
  readonly administrator: boolean;
}

export function visibleTabs(section: Section, viewer: Viewer): readonly SectionTab[] {
  return section.tabs.filter((tab) => isTierVisible(viewer.tier, tab.tier) && (viewer.administrator || !tab.administrator));
}

/** Where the address bar is, as far as tabs care. */
export interface Place {
  readonly path: string;
  readonly categoryId?: string;
}

export function isAt(tab: SectionTab, place: Place): boolean {
  if (place.path !== tab.path && !place.path.startsWith(`${tab.path}/`)) return false;
  return tab.search === undefined || tab.search.category_id === place.categoryId;
}

/** The section the current page belongs to, if any; the library sections are tried for the library in the address. */
export function sectionAt(place: Place, libraryId: string | undefined, kinds: readonly LibraryKind[] = []): Section | undefined {
  const candidates = [...(libraryId ? librarySections(libraryId, kinds) : []), ...GLOBAL_SECTIONS];
  return candidates.find((section) => section.tabs.some((tab) => isAt(tab, place)));
}

/** The tier at which the page at `path` is shown; "normal" for a page no section lists. */
export function pageTier(path: string): Tier {
  for (const section of GLOBAL_SECTIONS) {
    const tab = section.tabs.find((entry) => entry.path === path);
    if (tab) return tab.tier;
  }
  return "normal";
}

const LAST_TAB_PREFIX = "solarr.tab.";

/** The tab a section was last left on, so its sidebar entry returns there; undefined when none is remembered. */
export function lastTab(section: Section, viewer: Viewer): SectionTab | undefined {
  try {
    const label = localStorage.getItem(LAST_TAB_PREFIX + section.key);
    return visibleTabs(section, viewer).find((tab) => tab.label === label);
  } catch {
    return undefined;
  }
}

export function rememberTab(section: Section, tab: SectionTab): void {
  try {
    localStorage.setItem(LAST_TAB_PREFIX + section.key, tab.label);
  } catch {
    // Denied storage only loses the shortcut back to this tab.
  }
}

/** Where a section's sidebar entry leads: the tab last opened, else its first one the viewer may see. */
export function entryTab(section: Section, viewer: Viewer): SectionTab | undefined {
  return lastTab(section, viewer) ?? visibleTabs(section, viewer)[0];
}
