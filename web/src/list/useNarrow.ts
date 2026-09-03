import { useEffect, useState } from "react";

// Below this a dense table stops being readable and becomes cards (section 21.8).
export const NARROW = 700;

// Below this the sidebar collapses to icons (section 21.8's 700-1100px row).
export const SIDEBAR_COLLAPSE = 1100;

/** Tracks whether the viewport is at or below a max-width breakpoint. */
function useBelow(px: number): boolean {
  const [below, setBelow] = useState(() => matches(px));

  useEffect(() => {
    const query = window.matchMedia(`(max-width: ${px - 1}px)`);
    const update = () => setBelow(query.matches);
    query.addEventListener("change", update);
    // Between the first render and this effect the viewport may have changed.
    update();
    return () => query.removeEventListener("change", update);
  }, [px]);

  return below;
}

/** Tracks whether the viewport is below the card breakpoint. */
export function useNarrow(): boolean {
  return useBelow(NARROW);
}

/** Tracks whether the viewport is below the sidebar-collapse breakpoint. */
export function useSidebarCollapsed(): boolean {
  return useBelow(SIDEBAR_COLLAPSE);
}

function matches(px: number): boolean {
  // Server-side and pre-paint there is no window; desktop-first is the default.
  if (typeof window === "undefined" || !window.matchMedia) return false;
  return window.matchMedia(`(max-width: ${px - 1}px)`).matches;
}
