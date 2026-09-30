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
  { id: "lidarr", label: "Lidarr", base: "light" },
  { id: "lidarr-dark", label: "Lidarr dark", base: "dark" },
  { id: "sonarr", label: "Sonarr", base: "light" },
  { id: "sonarr-dark", label: "Sonarr dark", base: "dark" },
  { id: "radarr", label: "Radarr", base: "light" },
  { id: "radarr-dark", label: "Radarr dark", base: "dark" },
] as const satisfies readonly ThemeDefinition[];

/** A colour scheme names its light and dark variant; a scheme with only one ignores the mode.
 *  Mode and scheme are two settings (asked 2026-09-30), so "Lidarr" follows the OS like "Solarr". */
export interface SchemeDefinition {
  readonly id: string;
  readonly label: string;
  readonly light?: string;
  readonly dark?: string;
}

export const SCHEMES = [
  { id: "solarr", label: "Solarr", light: "light", dark: "dark" },
  { id: "lidarr", label: "Lidarr", light: "lidarr", dark: "lidarr-dark" },
  { id: "sonarr", label: "Sonarr", light: "sonarr", dark: "sonarr-dark" },
  { id: "radarr", label: "Radarr", light: "radarr", dark: "radarr-dark" },
  { id: "jellyfin", label: "Jellyfin", dark: "jellyfin" },
  { id: "arr", label: "Arr", dark: "arr" },
  { id: "nord", label: "Nord", dark: "nord" },
  { id: "paper", label: "Paper", light: "paper" },
] as const satisfies readonly SchemeDefinition[] as readonly SchemeDefinition[];

export type Mode = "system" | "light" | "dark";
export const MODES: readonly { id: Mode; label: string }[] = [
  { id: "system", label: "System" },
  { id: "light", label: "Light" },
  { id: "dark", label: "Dark" },
];

// index.html's pre-paint script reads the same two keys.
const MODE_KEY = "solarr.mode";
const SCHEME_KEY = "solarr.scheme";
const LEGACY_KEY = "solarr.theme";
const DEFAULT_BASE: ThemeDefinition = { id: "light", label: "Light", base: "light" };

export function definition(id: string): ThemeDefinition {
  return THEMES.find((theme) => theme.id === id) ?? DEFAULT_BASE;
}

export function scheme(id: string): SchemeDefinition {
  return SCHEMES.find((entry) => entry.id === id) ?? SCHEMES[0]!;
}

/** Whether a scheme has both variants, so the mode means something for it. */
export function followsMode(id: string): boolean {
  const entry = scheme(id);
  return entry.light !== undefined && entry.dark !== undefined;
}

export function resolve(mode: Mode, schemeID: string, prefersDark: boolean): ThemeDefinition {
  const entry = scheme(schemeID);
  const wantsDark = mode === "dark" || (mode === "system" && prefersDark);
  const id = (wantsDark ? entry.dark : entry.light) ?? entry.light ?? entry.dark ?? "light";
  return definition(id);
}

export function apply(mode: Mode, schemeID: string): void {
  const resolved = resolve(mode, schemeID, window.matchMedia("(prefers-color-scheme: dark)").matches);
  const root = document.documentElement;
  root.dataset.theme = resolved.id;
  // Kept in step so Tailwind's dark: variant still means what it says under a named dark theme.
  root.classList.toggle("dark", resolved.base === "dark");
  try {
    localStorage.setItem(MODE_KEY, mode);
    localStorage.setItem(SCHEME_KEY, scheme(schemeID).id);
    localStorage.removeItem(LEGACY_KEY);
  } catch {
    // Denied storage loses only the next load's pre-paint hint.
  }
}

/** The stored mode and scheme; a single theme stored before the split becomes its scheme and base. */
export function stored(): { mode: Mode; scheme: string } {
  try {
    const mode = localStorage.getItem(MODE_KEY);
    const schemeID = localStorage.getItem(SCHEME_KEY);
    if (mode || schemeID) {
      return {
        mode: MODES.some((entry) => entry.id === mode) ? (mode as Mode) : "system",
        scheme: scheme(schemeID ?? "").id,
      };
    }
    return fromLegacy(localStorage.getItem(LEGACY_KEY));
  } catch {
    return { mode: "system", scheme: "solarr" };
  }
}

export function fromLegacy(themeID: string | null): { mode: Mode; scheme: string } {
  const owner = SCHEMES.find((entry) => entry.light === themeID || entry.dark === themeID);
  if (!themeID || !owner) return { mode: "system", scheme: "solarr" };
  return { mode: definition(themeID).base, scheme: owner.id };
}
