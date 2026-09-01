import { useState } from "react";

import { apply, stored, type Theme } from "@/lib/theme";

const ORDER: Theme[] = ["system", "light", "dark"];
const LABEL: Record<Theme, string> = {
  system: "Follow the system",
  light: "Light",
  dark: "Dark",
};

// Three states rather than two: "follow the system" is a real choice, and a
// two-way toggle silently converts it into whichever the OS happened to be on
// the first click.
export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(stored);

  function advance() {
    const next = ORDER[(ORDER.indexOf(theme) + 1) % ORDER.length] ?? "system";
    setTheme(next);
    apply(next);
  }

  return (
    <button
      type="button"
      onClick={advance}
      title={LABEL[theme]}
      aria-label={`Theme: ${LABEL[theme]}. Click to change.`}
      className="rounded-md border border-border-subtle px-2.5 py-1 text-xs text-ink-muted
                 hover:bg-canvas hover:text-ink focus-visible:outline-2
                 focus-visible:outline-offset-2 focus-visible:outline-accent"
    >
      {LABEL[theme]}
    </button>
  );
}
