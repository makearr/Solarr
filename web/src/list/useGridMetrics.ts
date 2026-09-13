import { useEffect, useState, type RefObject } from "react";

export interface GridMetrics {
  /** How many posters fit across. Never below 1. */
  columns: number;
  /** The width one poster gets, so a square cover's row height is known
   *  before the virtualiser asks for it. */
  cellWidth: number;
}

/** Measures `ref` and works out how many posters of at least `minWidth` fit
 *  across it, with `gap` between them and `padding` either side. Remeasured
 *  as the element resizes. */
export function useGridMetrics(
  ref: RefObject<HTMLElement | null>,
  minWidth: number,
  gap: number,
  padding: number,
): GridMetrics {
  const [metrics, setMetrics] = useState<GridMetrics>({ columns: 1, cellWidth: minWidth });

  useEffect(() => {
    const element = ref.current;
    if (!element) return;

    const measure = (outerWidth: number) => {
      const available = Math.max(0, outerWidth - padding * 2);
      const columns = Math.max(1, Math.floor((available + gap) / (minWidth + gap)));
      const cellWidth = Math.max(1, (available - gap * (columns - 1)) / columns);
      setMetrics((current) =>
        current.columns === columns && Math.abs(current.cellWidth - cellWidth) < 1
          ? current
          : { columns, cellWidth },
      );
    };

    measure(element.clientWidth);
    const observer = new ResizeObserver((entries) => {
      const entry = entries[0];
      if (entry) measure(entry.contentRect.width);
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [ref, minWidth, gap, padding]);

  return metrics;
}
