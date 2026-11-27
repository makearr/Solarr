/** One global shortcut as the help overlay lists it. */
export interface ShortcutInfo {
  readonly keys: string;
  readonly label: "palette" | "dashboard" | "library" | "settings" | "next" | "previous" | "open" | "help";
}

export const SHORTCUTS: readonly ShortcutInfo[] = [
  { keys: "Ctrl K  /  /", label: "palette" },
  { keys: "g d", label: "dashboard" },
  { keys: "g l", label: "library" },
  { keys: "g s", label: "settings" },
  { keys: "j", label: "next" },
  { keys: "k", label: "previous" },
  { keys: "Enter", label: "open" },
  { keys: "?", label: "help" },
];

/** Whether a key press happened while typing, when single-letter shortcuts must stay out of the way. */
export function isTyping(target: EventTarget | null): boolean {
  const element = target as HTMLElement | null;
  if (element === null || typeof element.tagName !== "string") return false;
  return element.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(element.tagName);
}

/** The window in which the second key of a two-key sequence ("g" then "d") still counts. */
export const SEQUENCE_MS = 1200;

/** The key that follows "g", and where it goes. */
export const GO_TARGETS: Readonly<Record<string, "dashboard" | "library" | "settings">> = { d: "dashboard", l: "library", s: "settings" };

/** The row before or after the focused one in a list; the first row when none is focused, undefined past either end. */
export function neighbour<T extends Element>(rows: readonly T[], focused: Element | null, step: 1 | -1): T | undefined {
  const at = focused === null ? -1 : rows.findIndex((row) => row === focused || row.contains(focused));
  if (at < 0) return step === 1 ? rows[0] : rows.at(-1);
  return rows[at + step];
}
