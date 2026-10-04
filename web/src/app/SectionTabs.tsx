import { useEffect } from "react";
import { Link, useParams, useRouterState, useSearch } from "@tanstack/react-router";

import { useIsAdministrator } from "@/lib/useAdministrator";
import { useLibrary } from "@/lib/useLibrary";
import { useTier } from "@/lib/useTier";

import { isAt, rememberTab, sectionAt, visibleTabs } from "./sections";

/** The tabs of the section the current page belongs to: one sidebar entry, its pages side by side here
 *  (2026-10-04: the sidebar listed every page and was overloaded). Nothing is shown for a page in no section. */
export function SectionTabs() {
  const tier = useTier();
  const administrator = useIsAdministrator();
  const path = useRouterState({ select: (state) => state.location.pathname });
  const params = useParams({ strict: false }) as { categoryId?: string };
  const search = useSearch({ strict: false }) as { category_id?: string };
  const libraryId = params.categoryId ?? (path === "/unidentified" ? search.category_id : undefined);
  const library = useLibrary(libraryId);

  const place = { path, categoryId: search.category_id };
  const section = sectionAt(place, libraryId);
  const tabs = section ? visibleTabs(section, { tier, administrator }) : [];
  const active = tabs.find((tab) => isAt(tab, place));

  useEffect(() => {
    if (section && active) rememberTab(section, active);
  }, [section, active]);

  if (!section || tabs.length < 2) return null;
  return (
    <nav
      aria-label={library ? `${library.name}: ${section.label}` : section.label}
      className="sticky top-0 z-10 flex flex-wrap items-end gap-x-1 border-b border-border-subtle bg-canvas px-4 pt-2 text-sm"
    >
      {library && <span className="mr-2 pb-1.5 text-xs text-ink-muted">{library.name}</span>}
      {tabs.map((tab) => (
        <span key={tab.label} className="flex items-end">
          {tab.heading && <span className="mx-2 pb-1.5 text-[0.6875rem] uppercase tracking-wide text-ink-muted">{tab.heading}</span>}
          <Link
            to={tab.path}
            search={tab.search}
            aria-current={tab === active ? "page" : undefined}
            className={`-mb-px rounded-t-md border-b-2 px-3 py-1.5 focus-visible:outline-2 focus-visible:outline-offset-2
                        focus-visible:outline-accent ${
                          tab === active
                            ? "border-accent font-medium text-ink"
                            : "border-transparent text-ink-muted hover:text-ink"
                        }`}
          >
            {tab.label}
          </Link>
        </span>
      ))}
    </nav>
  );
}
