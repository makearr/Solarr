import { usePreference } from "./usePreference";

/** Whether the sidebar lists Calendar; Settings -> Web UI, on by default (2026-10-23). */
export function useShowCalendar(): [boolean, (value: boolean) => void] {
  const [shown, set] = usePreference("ui.show_calendar", true);
  return [shown, set];
}

/** Whether the status bar runs along the bottom; Settings -> Web UI, on by default (2026-10-23). */
export function useShowFooter(): [boolean, (value: boolean) => void] {
  const [shown, set] = usePreference("ui.show_footer", true);
  return [shown, set];
}

/** What the status bar can show, each its own switch and place in Settings -> Web UI (2026-10-24). */
export const FOOTER_ITEMS = ["events", "work", "queue", "ip", "time", "version"] as const;
export type FooterItem = (typeof FOOTER_ITEMS)[number];
export interface FooterEntry {
  readonly item: FooterItem;
  readonly shown: boolean;
}

// The address and the clock start hidden so a screenshot gives neither away.
const HIDDEN_BY_DEFAULT: ReadonlySet<FooterItem> = new Set(["ip", "time"]);
export const DEFAULT_FOOTER: readonly FooterEntry[] = FOOTER_ITEMS.map((item) => ({ item, shown: !HIDDEN_BY_DEFAULT.has(item) }));

/** The stored order, unknown items dropped and ones added since appended with their default. */
export function normaliseFooter(stored: readonly FooterEntry[]): FooterEntry[] {
  const known = stored.filter((entry, at) => FOOTER_ITEMS.includes(entry.item) && stored.findIndex((other) => other.item === entry.item) === at);
  return [...known, ...DEFAULT_FOOTER.filter((entry) => !known.some((other) => other.item === entry.item))];
}

/** The status bar's items in order with whether each shows; Settings -> Web UI. */
export function useFooterItems(): [FooterEntry[], (value: FooterEntry[]) => void] {
  const [stored, set] = usePreference<FooterEntry[]>("ui.footer_items", [...DEFAULT_FOOTER]);
  return [normaliseFooter(Array.isArray(stored) ? stored : []), set];
}
