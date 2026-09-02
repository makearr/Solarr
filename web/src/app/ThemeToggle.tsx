import { useEffect, useState } from "react";
import { Palette } from "lucide-react";

import { apply, stored, THEMES, type Theme } from "@/lib/theme";

/** A picker rather than a switch: themes are named sets, and "system" is the
 *  only one that follows the OS. */
export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(() => stored());

  useEffect(() => apply(theme), [theme]);

  // A stored theme wins; only "system" should follow the OS changing.
  useEffect(() => {
    if (theme !== "system") return;
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const follow = () => apply("system");
    media.addEventListener("change", follow);
    return () => media.removeEventListener("change", follow);
  }, [theme]);

  return (
    <label className="flex items-center gap-1.5 text-ink-muted" title="Theme">
      <Palette aria-hidden className="size-3.5" />
      <span className="sr-only">Theme</span>
      <select
        value={theme}
        onChange={(event) => setTheme(event.target.value)}
        className="rounded-md border border-border-subtle bg-canvas px-1.5 py-0.5 text-xs
                   text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                   focus-visible:outline-accent"
      >
        <option value="system">System</option>
        {THEMES.map((entry) => (
          <option key={entry.id} value={entry.id}>
            {entry.label}
          </option>
        ))}
      </select>
    </label>
  );
}
