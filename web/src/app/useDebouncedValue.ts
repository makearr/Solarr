import { useEffect, useState } from "react";

/** Delays reflecting value by delayMs, so a fast typist does not fire one request per
 *  keystroke. Small enough not to need a dependency (SearchBox's own 250ms). */
export function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);

  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(timer);
  }, [value, delayMs]);

  return debounced;
}
