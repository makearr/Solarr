import { useEffect } from "react";
import { useParams, useRouterState, useSearch } from "@tanstack/react-router";

import { useIsAdministrator } from "@/lib/useAdministrator";
import { useLibraryKinds } from "@/lib/libraryLabel";
import { useLibrary } from "@/lib/useLibrary";
import { useTier } from "@/lib/useTier";
import { TabRow } from "@/ui/TabRow";

import { isAt, rememberTab, sectionAt, visibleTabs } from "./sections";

/** The pages of the section the current page belongs to, side by side above it. Settings lists its pages in the
 *  sidebar instead; nothing is shown for a page in no section. */
export function SectionTabs() {
  const tier = useTier();
  const administrator = useIsAdministrator();
  const path = useRouterState({ select: (state) => state.location.pathname });
  const params = useParams({ strict: false }) as { categoryId?: string };
  const search = useSearch({ strict: false }) as { category_id?: string };
  const libraryId = params.categoryId ?? (path === "/unidentified" ? search.category_id : undefined);
  const library = useLibrary(libraryId);
  const { libraryId: head, kinds } = useLibraryKinds(libraryId);

  const place = { path, categoryId: search.category_id };
  const section = sectionAt(place, head, kinds);
  const tabs = section ? visibleTabs(section, { tier, administrator }) : [];
  const active = tabs.find((tab) => isAt(tab, place));

  useEffect(() => {
    if (section && active) rememberTab(section, active);
  }, [section, active]);

  if (!section || tabs.length < 2 || section.key === "settings") return null;
  return (
    <TabRow
      sticky
      label={library ? `${library.name}: ${section.label}` : section.label}
      lead={library ? section.label : undefined}
      items={tabs.map((tab) => ({ key: tab.label, label: tab.label, to: tab.path, search: tab.search, active: tab === active }))}
    />
  );
}
