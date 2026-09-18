/** Named themes, not just light and dark. The tokens live in index.css; this
 *  file only decides which set is active and remembers the choice.
 *  See documentation/web-interface.md. */

export interface ThemeDefinition {
  readonly id: string;
  readonly label: string;
  /** Which base a theme is a variation of, so anything that has to branch on
   *  light against dark -- an embedded map, a syntax block -- can still ask. */
  readonly base: "light" | "dark";
}

export const THEMES = [
  { id: "light", label: "Light", base: "light" },
  { id: "dark", label: "Dark", base: "dark" },
  { id: "jellyfin", label: "Jellyfin", base: "dark" },
  { id: "arr", label: "Arr", base: "dark" },
  { id: "nord", label: "Nord", base: "dark" },
  { id: "paper", label: "Paper", base: "light" },
] as const satisfies readonly ThemeDefinition[];

/** "system" follows the OS and resolves to light or dark, never to a named theme:
 *  the OS reports a preference between two, and has no opinion about Jellyfin. */
export type Theme = "system" | (string & {});

const STORAGE_KEY = "solarr.theme";
const DEFAULT_BASE: ThemeDefinition = { id: "light", label: "Light", base: "light" };

export function definition(id: string): ThemeDefinition {
  return THEMES.find((theme) => theme.id === id) ?? DEFAULT_BASE;
}

export function resolve(theme: Theme): ThemeDefinition {
  if (theme === "system") {
    return window.matchMedia("(prefers-color-scheme: dark)").matches
      ? definition("dark")
      : definition("light");
  }
  return definition(theme);
}

export function apply(theme: Theme): void {
  const resolved = resolve(theme);
  const root = document.documentElement;
  root.dataset.theme = resolved.id;
  // Kept in step so Tailwind's dark: variant still means what it says under a
  // named dark theme, rather than only under the one called "dark".
  root.classList.toggle("dark", resolved.base === "dark");
  try {
    if (theme === "system") localStorage.removeItem(STORAGE_KEY);
    else localStorage.setItem(STORAGE_KEY, theme);
  } catch {
    // Denied storage loses only the next load's pre-paint hint.
  }
}

export function stored(): Theme {
  try {
    const value = localStorage.getItem(STORAGE_KEY);
    return value && THEMES.some((theme) => theme.id === value) ? value : "system";
  } catch {
    return "system";
  }
}
