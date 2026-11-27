import { CircleHelp } from "lucide-react";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";

import { t } from "@/lib/i18n";

/** A "?" beside a label that opens its help: on hover or focus for a mouse, on tap for a touch screen, closed by Escape or a tap elsewhere. */
export function HelpTip({ children }: { children: ReactNode }) {
  const [pinned, setPinned] = useState(false);
  const [hovered, setHovered] = useState(false);
  const wrapper = useRef<HTMLSpanElement>(null);
  const id = useId();
  const open = pinned || hovered;

  useEffect(() => {
    if (!pinned) return;
    const away = (event: Event) => {
      if (event.target instanceof Node && !wrapper.current?.contains(event.target)) setPinned(false);
    };
    const escape = (event: KeyboardEvent) => event.key === "Escape" && setPinned(false);
    document.addEventListener("pointerdown", away);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("pointerdown", away);
      document.removeEventListener("keydown", escape);
    };
  }, [pinned]);

  return (
    <span ref={wrapper} className="relative inline-flex" onMouseEnter={() => setHovered(true)} onMouseLeave={() => setHovered(false)}>
      <button
        type="button"
        aria-label={t("help.label")}
        aria-expanded={open}
        aria-describedby={open ? id : undefined}
        onClick={() => setPinned(!pinned)}
        className="rounded-full text-ink-muted hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        <CircleHelp aria-hidden className="size-4" />
      </button>
      {open && (
        <span
          id={id}
          role="tooltip"
          className="absolute left-0 top-full z-30 mt-1 w-72 max-w-[calc(100vw-2rem)] rounded-md border border-border-subtle bg-surface p-2 text-xs font-normal normal-case text-ink shadow-lg"
        >
          {children}
        </span>
      )}
    </span>
  );
}
