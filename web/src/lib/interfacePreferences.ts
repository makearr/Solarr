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

/** Whether a video's preview pictures show beside its row; Settings -> Web UI, on by default (2026-11-07). */
export function useShowThumbnails(): [boolean, (value: boolean) => void] {
  const [shown, set] = usePreference("ui.show_thumbnails", true);
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

/** The dashboard's sections, in their default order; the first two are the administrator's. */
export const DASHBOARD_WIDGETS = ["setup", "attention", "scanning", "downloads", "discover", "recent", "libraries"] as const;
export type DashboardWidget = (typeof DASHBOARD_WIDGETS)[number];
export const ADMINISTRATOR_WIDGETS: ReadonlySet<DashboardWidget> = new Set(["setup", "attention"]);
export interface DashboardEntry {
  readonly widget: DashboardWidget;
  readonly shown: boolean;
}
export const DEFAULT_DASHBOARD: readonly DashboardEntry[] = DASHBOARD_WIDGETS.map((widget) => ({ widget, shown: true }));

/** The stored order, unknown widgets dropped and ones added since appended shown. */
export function normaliseDashboard(stored: readonly DashboardEntry[]): DashboardEntry[] {
  const known = stored.filter((entry, at) => DASHBOARD_WIDGETS.includes(entry.widget) && stored.findIndex((other) => other.widget === entry.widget) === at);
  return [...known, ...DEFAULT_DASHBOARD.filter((entry) => !known.some((other) => other.widget === entry.widget))];
}

/** The caller's dashboard sections in order with whether each shows; Settings -> Web UI. */
export function useDashboardWidgets(): [DashboardEntry[], (value: DashboardEntry[]) => void] {
  const [stored, set] = usePreference<DashboardEntry[]>("ui.dashboard_widgets", [...DEFAULT_DASHBOARD]);
  return [normaliseDashboard(Array.isArray(stored) ? stored : []), set];
}

/** Whether the administrator closed the setup checklist before finishing it. */
export function useSetupDismissed(): [boolean, (value: boolean) => void] {
  const [dismissed, set] = usePreference("ui.setup_dismissed", false);
  return [dismissed, set];
}
