import { useEffect, useSyncExternalStore } from "react";

import { apply, followsMode, resolve, stored, type Mode } from "./theme";

interface ThemeChoice {
  readonly mode: Mode;
  readonly scheme: string;
}

// One choice for the whole page: the top-bar switch and the user menu must always agree.
let choice: ThemeChoice = stored();
const listeners = new Set<() => void>();

function choose(next: ThemeChoice): void {
  choice = next;
  apply(next.mode, next.scheme);
  listeners.forEach((listener) => listener());
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function prefersDark(): boolean {
  return window.matchMedia("(prefers-color-scheme: dark)").matches;
}

/** The mode a click on the light/dark switch moves to: the opposite of what is showing now. */
export function flipped(mode: Mode, scheme: string, osPrefersDark: boolean): Mode {
  return resolve(mode, scheme, osPrefersDark).base === "dark" ? "light" : "dark";
}

/** The appearance in effect and the ways to change it, shared by every control that shows it. */
export function useTheme() {
  const current = useSyncExternalStore(subscribe, () => choice);

  useEffect(() => apply(current.mode, current.scheme), [current]);

  // Only "system" follows the OS changing.
  useEffect(() => {
    if (current.mode !== "system") return;
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const follow = () => choose({ ...choice });
    media.addEventListener("change", follow);
    return () => media.removeEventListener("change", follow);
  }, [current.mode]);

  return {
    mode: current.mode,
    scheme: current.scheme,
    /** False for a colour scheme with one variant, where light or dark means nothing. */
    hasBothModes: followsMode(current.scheme),
    showingDark: resolve(current.mode, current.scheme, prefersDark()).base === "dark",
    setMode: (mode: Mode) => choose({ ...choice, mode }),
    setScheme: (scheme: string) => choose({ ...choice, scheme }),
    flip: () => choose({ ...choice, mode: flipped(choice.mode, choice.scheme, prefersDark()) }),
  };
}
