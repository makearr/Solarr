import { useCallback, useEffect, useState } from "react";

const STORAGE_KEY = "medianexus.sidebar.open";

/** Reads the remembered open set, or null when nothing is remembered yet, so a
 *  first visit can start from sensible defaults. Corrupt storage counts as
 *  nothing remembered, the same tolerance theme.ts has. */
function load(): string[] | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((key): key is string => typeof key === "string") : null;
  } catch {
    return null;
  }
}

/** Which sidebar submenus are expanded, by key. Replaces the path-driven
 *  "only the branch you are standing in is open" rule, which made it
 *  impossible to keep two sections open at once (asked for 2026-09-13).
 *  Several may be open together, the set survives a reload, and the branch
 *  the caller navigates into opens itself without closing the others. */
export function useOpenSections(activeKeys: readonly string[], defaultOpen: readonly string[] = []) {
  const [open, setOpen] = useState<Set<string>>(() => new Set(load() ?? defaultOpen));
  const activeSignature = activeKeys.join("|");

  // Navigating into a branch opens it, additively -- following a deep link
  // still reveals where you landed without collapsing what you left open.
  useEffect(() => {
    const keys = activeSignature === "" ? [] : activeSignature.split("|");
    if (keys.length === 0) return;
    setOpen((current) => {
      const missing = keys.filter((key) => !current.has(key));
      if (missing.length === 0) return current;
      return new Set([...current, ...missing]);
    });
  }, [activeSignature]);

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify([...open]));
    } catch {
      // A browser refusing storage costs the memory of the open set, nothing more.
    }
  }, [open]);

  const toggle = useCallback((key: string) => {
    setOpen((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }, []);

  return { isOpen: (key: string) => open.has(key), toggle };
}
