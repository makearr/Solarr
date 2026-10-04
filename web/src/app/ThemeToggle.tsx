import { Moon, Sun } from "lucide-react";

import { useTheme } from "@/lib/useTheme";

/** The one appearance control in the top bar: a click flips light and dark. System, and the colour scheme, are
 *  in the user menu under Appearance (2026-10-04: two pickers here overloaded the bar). */
export function ThemeToggle() {
  const theme = useTheme();
  const label = theme.hasBothModes
    ? theme.showingDark ? "Switch to light" : "Switch to dark"
    : "This colour scheme has only one variant";
  const Icon = theme.showingDark ? Sun : Moon;
  return (
    <button
      type="button"
      onClick={theme.flip}
      disabled={!theme.hasBothModes}
      aria-label={label}
      title={label}
      className="rounded-md p-1.5 text-ink-muted hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                 focus-visible:outline-accent disabled:opacity-50"
    >
      <Icon aria-hidden className="size-4" />
    </button>
  );
}
