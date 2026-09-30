import { useEffect, useState } from "react";
import { Palette, SunMoon } from "lucide-react";

import { apply, followsMode, MODES, SCHEMES, stored, type Mode } from "@/lib/theme";

const selectStyle =
  "rounded-md border border-border-subtle bg-canvas px-1.5 py-0.5 text-xs text-ink focus-visible:outline-2 " +
  "focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50";

/** Two pickers (asked 2026-09-30): light, dark or the OS's choice, and the colour scheme. */
export function ThemeToggle() {
  const [initial] = useState(() => stored());
  const [mode, setMode] = useState<Mode>(initial.mode);
  const [scheme, setScheme] = useState(initial.scheme);

  useEffect(() => apply(mode, scheme), [mode, scheme]);

  // Only "system" follows the OS changing.
  useEffect(() => {
    if (mode !== "system") return;
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const follow = () => apply("system", scheme);
    media.addEventListener("change", follow);
    return () => media.removeEventListener("change", follow);
  }, [mode, scheme]);

  const single = !followsMode(scheme);
  return (
    <div className="flex items-center gap-2 text-ink-muted">
      <label className="flex items-center gap-1.5" title={single ? "This colour scheme has only one variant" : "Light or dark"}>
        <SunMoon aria-hidden className="size-3.5" />
        <span className="sr-only">Light or dark</span>
        <select value={mode} disabled={single} onChange={(event) => setMode(event.target.value as Mode)} className={selectStyle}>
          {MODES.map((entry) => (
            <option key={entry.id} value={entry.id}>
              {entry.label}
            </option>
          ))}
        </select>
      </label>
      <label className="flex items-center gap-1.5" title="Colour scheme">
        <Palette aria-hidden className="size-3.5" />
        <span className="sr-only">Colour scheme</span>
        <select value={scheme} onChange={(event) => setScheme(event.target.value)} className={selectStyle}>
          {SCHEMES.map((entry) => (
            <option key={entry.id} value={entry.id}>
              {entry.label}
            </option>
          ))}
        </select>
      </label>
    </div>
  );
}
