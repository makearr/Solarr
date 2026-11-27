import { useParams, useRouterState, useSearch } from "@tanstack/react-router";

import { t } from "@/lib/i18n";
import { useLibraryKinds } from "@/lib/libraryLabel";
import { useDiscoverLibraries, useLibraryTree } from "@/lib/resources";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { useTier } from "@/lib/useTier";
import { TabRow, type TabRowItem } from "@/ui/TabRow";

import { defaultLevelKey } from "./Sidebar";
import { entryTab, isAt, librarySections, sectionAt, visibleTabs } from "./sections";

/** A library's own pages as one tab row: its levels, what it lacks, how it stands, its files, its settings. */
export function LibraryTabs() {
  const tier = useTier();
  const administrator = useIsAdministrator();
  const path = useRouterState({ select: (state) => state.location.pathname });
  const params = useParams({ strict: false }) as { categoryId?: string; level?: string };
  const search = useSearch({ strict: false }) as { category_id?: string };
  const libraryId = params.categoryId ?? (path === "/unidentified" ? search.category_id : undefined);
  const { libraryId: head, kinds } = useLibraryKinds(libraryId);
  const tree = useLibraryTree().data;
  const recommended = useDiscoverLibraries().data;

  const viewer = { tier, administrator };
  const place = { path, categoryId: search.category_id };
  const library = tree?.find((entry) => String(entry.id) === head);
  const inSection = sectionAt(place, head, kinds)?.key.startsWith("library:") ?? false;
  if (!head || !library || (!params.level && !inSection)) return null;

  const level = defaultLevelKey(library);
  const items: TabRowItem[] = [];
  if (level) {
    items.push({ key: "browse", label: t("libraryTabs.browse"), to: `/library/${head}/${level}`, active: !inSection });
  }
  if (recommended?.some((entry) => entry.category_id === library.id)) {
    items.push({ key: "recommended", label: t("discover.recommended"), to: "/discover", search: { library: library.id }, active: false });
  }
  for (const section of librarySections(head, kinds)) {
    const tab = entryTab(section, viewer);
    if (!tab) continue;
    const kind = section.key.slice(section.key.lastIndexOf(":") + 1);
    items.push({
      key: section.key,
      label: section.label,
      to: tab.path,
      search: tab.search,
      active: visibleTabs(section, viewer).some((entry) => isAt(entry, place)),
      badge: kind === "wanted" ? library.wanted : kind === "watched" ? library.watched : undefined,
    });
  }
  return <TabRow label={library.name} lead={library.name} items={items} />;
}
