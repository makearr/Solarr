import { useEffect, useState } from "react";

// Below this a dense table stops being readable and becomes cards (section 21.8).
export const NARROW = 700;

/** Tracks whether the viewport is below the card breakpoint. */
export function useNarrow(): boolean {
  const [narrow, setNarrow] = useState(() => matches());

  useEffect(() => {
    const query = window.matchMedia(`(max-width: ${NARROW - 1}px)`);
    const update = () => setNarrow(query.matches);
    query.addEventListener("change", update);
    // Between the first render and this effect the viewport may have changed.
    update();
    return () => query.removeEventListener("change", update);
  }, []);

  return narrow;
}

function matches(): boolean {
  // Server-side and pre-paint there is no window; desktop-first is the default.
  if (typeof window === "undefined" || !window.matchMedia) return false;
  return window.matchMedia(`(max-width: ${NARROW - 1}px)`).matches;
}
