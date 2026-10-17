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
  /** A small heading before this tab, for a long row. */
  readonly heading?: string;
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
    { label: "Notifications", path: "/activity/notifications", tier: "normal" },
    { label: "Jobs", path: "/activity/jobs", tier: "advanced" },
  ],
};

export const STORAGE: Section = {
  key: "storage",
  label: "Storage",
  tabs: [
    { label: "Root folders", path: "/storage/roots", tier: "advanced", administrator: true },
    { label: "Download folders", path: "/storage/downloads", tier: "advanced", administrator: true },
    { label: "Import", path: "/storage/import", tier: "advanced", administrator: true },
    { label: "Recycle bin", path: "/storage/recycle-bin", tier: "advanced", administrator: true },
    // Every library's waiting files at once; one library's are under its own Files.
    { label: "Unidentified", path: "/unidentified", tier: "advanced" },
  ],
};

export const CONNECTIONS: Section = {
  key: "connections",
  label: "Connections",
  tabs: [
    { label: "Indexers", path: "/connections/indexers", tier: "advanced", administrator: true },
    { label: "Download clients", path: "/connections/downloaders", tier: "advanced", administrator: true },
    { label: "Arr", path: "/connections/arr", tier: "expert", administrator: true },
  ],
};

export const SETTINGS: Section = {
  key: "settings",
  label: "Settings",
  tabs: [
    { label: "Libraries", path: "/settings/libraries", tier: "normal", administrator: true, heading: "General" },
    { label: "Tags", path: "/settings/tags", tier: "normal", administrator: true },
    { label: "Blocklists", path: "/settings/blocklists", tier: "expert", administrator: true },
    { label: "User", path: "/settings/user", tier: "normal" },
    { label: "Sources", path: "/settings/metadata", tier: "normal", administrator: true, heading: "Metadata" },
    { label: "Quality profiles", path: "/quality-profiles", tier: "advanced", administrator: true },
    { label: "Network", path: "/settings/network", tier: "advanced", administrator: true, heading: "System" },
    { label: "Scheduler", path: "/settings/scheduler", tier: "normal", administrator: true },
    { label: "Backups", path: "/settings/backups", tier: "normal", administrator: true },
    { label: "Cache", path: "/settings/cache", tier: "expert", administrator: true },
    { label: "Logs", path: "/settings/logs", tier: "advanced", administrator: true },
  ],
};

export const GLOBAL_SECTIONS: readonly Section[] = [ACTIVITY, STORAGE, CONNECTIONS, SETTINGS];

/** A library's four entries after its own levels: what it lacks, what happened and how it stands, its files, its
 *  configuration. */
export function librarySections(categoryId: string): readonly Section[] {
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
        { label: "Packs", path: `${base}/packs`, tier: "expert" },
      ],
    },
    {
      key: `library:${categoryId}:settings`,
      label: "Settings",
      tabs: [
        { label: "Quality", path: `${base}/config/quality`, tier: "advanced", administrator: true },
        { label: "Naming", path: `${base}/config/naming`, tier: "advanced", administrator: true },
        { label: "Metadata", path: `${base}/config/metadata`, tier: "advanced", administrator: true },
        { label: "File management", path: `${base}/config/file-management`, tier: "expert", administrator: true },
        { label: "Import lists", path: `${base}/config/import-lists`, tier: "advanced", administrator: true },
      ],
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
export function sectionAt(place: Place, libraryId: string | undefined): Section | undefined {
  const candidates = [...(libraryId ? librarySections(libraryId) : []), ...GLOBAL_SECTIONS];
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
