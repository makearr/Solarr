import { useEffect, useRef } from "react";
import { useNavigate } from "@tanstack/react-router";

import { useLibraryTree } from "@/lib/resources";
import { setOverlay } from "@/lib/overlays";
import { GO_TARGETS, isTyping, neighbour, SEQUENCE_MS } from "@/lib/shortcuts";

import { defaultLevelKey } from "./Sidebar";

const ROWS = 'main [role="row"][aria-selected], main [role="button"][aria-pressed]';

/** The keyboard layer: Ctrl+K or "/" opens the palette, "?" the shortcut list, "g" then d, l or s goes to the Dashboard, the first library
 *  or Settings, and j / k / Enter walk the rows of a list. Renders nothing. */
export function GlobalShortcuts() {
  const navigate = useNavigate();
  const libraries = useLibraryTree();
  const first = libraries.data?.[0];
  const firstLevel = first ? defaultLevelKey(first) : undefined;
  const pendingGo = useRef(0);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setOverlay("palette");
        return;
      }
      if (event.ctrlKey || event.metaKey || event.altKey || isTyping(event.target)) return;
      // A dialog or menu owns the keyboard while it is open.
      if (document.querySelector('[role="dialog"], [role="menu"]')) return;

      const goingTo = Date.now() - pendingGo.current < SEQUENCE_MS ? GO_TARGETS[event.key] : undefined;
      pendingGo.current = 0;
      if (goingTo) {
        event.preventDefault();
        if (goingTo === "dashboard") void navigate({ to: "/" });
        else if (goingTo === "settings") void navigate({ to: "/settings" });
        else if (first && firstLevel) void navigate({ to: "/library/$categoryId/$level", params: { categoryId: String(first.id), level: firstLevel } });
        return;
      }
      if (event.key === "g") pendingGo.current = Date.now();
      else if (event.key === "/") {
        event.preventDefault();
        setOverlay("palette");
      } else if (event.key === "?") setOverlay("shortcuts");
      else if (event.key === "j" || event.key === "k") {
        const rows = [...document.querySelectorAll<HTMLElement>(ROWS)];
        const next = neighbour(rows, document.activeElement, event.key === "j" ? 1 : -1);
        if (!next) return;
        event.preventDefault();
        next.focus();
        next.scrollIntoView({ block: "nearest" });
      } else if (event.key === "Enter") {
        const row = document.activeElement;
        if (row instanceof HTMLElement && row.matches(ROWS)) row.querySelector<HTMLAnchorElement>("a")?.click();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [navigate, first, firstLevel]);

  return null;
}
