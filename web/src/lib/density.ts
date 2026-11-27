import { useEffect } from "react";

import { usePreference } from "./usePreference";

export type Density = "comfortable" | "compact";

/** How much a compact list shrinks its rows and posters. */
const COMPACT_SCALE = 0.85;

/** The caller's list density; Settings -> Web UI, comfortable by default. */
export function useDensity(): [Density, (value: Density) => void] {
  const [density, set] = usePreference<Density>("ui.density", "comfortable");
  return [density === "compact" ? "compact" : "comfortable", set];
}

/** The factor row heights and poster widths are multiplied by. */
export function useDensityScale(): number {
  return useDensity()[0] === "compact" ? COMPACT_SCALE : 1;
}

/** Puts the density on <html> so plain CSS can follow it too (data-density, --density-scale). */
export function useApplyDensity(): void {
  const scale = useDensityScale();
  useEffect(() => {
    const root = document.documentElement;
    root.dataset.density = scale === 1 ? "comfortable" : "compact";
    root.style.setProperty("--density-scale", String(scale));
  }, [scale]);
}
