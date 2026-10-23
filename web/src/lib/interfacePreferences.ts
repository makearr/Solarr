import { usePreference } from "./usePreference";

/** Whether the sidebar lists Calendar; Settings -> Web UI, on by default (2026-10-23). */
export function useShowCalendar(): [boolean, (value: boolean) => void] {
  return usePreference("ui.show_calendar", true);
}

/** Whether the status bar runs along the bottom; Settings -> Web UI, on by default (2026-10-23). */
export function useShowFooter(): [boolean, (value: boolean) => void] {
  return usePreference("ui.show_footer", true);
}
