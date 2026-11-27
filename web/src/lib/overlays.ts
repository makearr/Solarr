import { useSyncExternalStore } from "react";

export type Overlay = "palette" | "shortcuts";

let open: Overlay | null = null;
const listeners = new Set<() => void>();

/** Which of the two global overlays (command palette, shortcut list) is showing; the header button and the keys share it. */
export function setOverlay(next: Overlay | null): void {
  open = next;
  listeners.forEach((listener) => listener());
}

export function useOverlay(): Overlay | null {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => open,
  );
}
