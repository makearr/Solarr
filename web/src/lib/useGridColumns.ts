import { useCallback, useEffect, useState } from "react";

/** How many columns of at least `min` px fit the element's width with `gap` between them; the caller attaches `ref`. */
export function useGridColumns(min: number, gap: number): { ref: (element: HTMLElement | null) => void; columns: number } {
  const [element, setElement] = useState<HTMLElement | null>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    if (!element) return;
    const observer = new ResizeObserver(([entry]) => setWidth(entry?.contentRect.width ?? 0));
    observer.observe(element);
    return () => observer.disconnect();
  }, [element]);
  const ref = useCallback((next: HTMLElement | null) => setElement(next), []);
  return { ref, columns: Math.max(1, Math.floor((width + gap) / (min + gap))) };
}
