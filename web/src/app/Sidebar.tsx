import { Link, useParams, useRouterState, useSearch } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  ArrowLeftRight,
  ChevronDown,
  ChevronRight,
  Download,
  HardDrive,
  HelpCircle,
  LayoutDashboard,
  Library as LibraryIcon,
  Radar,
  ScanSearch,
  Settings,
  Share2,
  type LucideIcon,
} from "lucide-react";

import { api } from "@/api/client";
import { useSidebarCollapsed } from "@/list/useNarrow";
import { mediaTypeIcon } from "@/lib/mediaTypeIcon";
import { useOpenSections } from "@/lib/useOpenSections";
import { isTierVisible, useTier, type Tier } from "@/lib/useTier";
import type { components } from "@/api/schema";

import { ThemeToggle } from "./ThemeToggle";

type LibraryEntry = components["schemas"]["Library"];

// People stopped being a top-level section 2026-09-09 (ADR-54's reversal):
// a person now surfaces under their own library (Music -> Artists), and
// global search still spans every category.
const SECTIONS: readonly { to: string; label: string; icon: LucideIcon; tier: Tier }[] = [
  { to: "/unidentified", label: "Unidentified", icon: HelpCircle, tier: "advanced" },
  { to: "/activity", label: "Activity", icon: Activity, tier: "advanced" },
];

const STORAGE_SECTIONS: readonly { to: string; label: string; icon: LucideIcon }[] = [
  { to: "/storage/roots", label: "Root folders", icon: HardDrive },
  { to: "/storage/moves", label: "Move & drain", icon: ArrowLeftRight },
  { to: "/storage/downloads", label: "Download folders", icon: Download },
  { to: "/storage/import", label: "Import", icon: ScanSearch },
];

const CONNECTIONS_SECTIONS: readonly { to: string; label: string; icon: LucideIcon }[] = [
  { to: "/connections/indexers", label: "Indexers", icon: Radar },
  { to: "/connections/downloaders", label: "Download clients", icon: Download },
];

// tier is the whole section's own floor (ADR-26, todo 4.14): General mixes
// normal and advanced content and always shows, but Cache's entire content is
// expert-only, so the nav entry itself disappears below that tier.
const SETTINGS_SECTIONS: readonly { to: string; label: string; tier?: Tier }[] = [
  { to: "/settings/general", label: "General" },
  { to: "/settings/user", label: "User" },
  { to: "/settings/scheduler", label: "Scheduler" },
  // Advanced: an install with no acquisition yet has an empty blocklist and
  // no way to fill one.
  { to: "/settings/blocklist", label: "Blocklist", tier: "advanced" },
  { to: "/settings/logs", label: "Logs", tier: "advanced" },
  // Normal: whether your data is safe is not an advanced question. Cache is
  // expert because it is tuning; this is not.
  { to: "/settings/backups", label: "Backups" },
  { to: "/settings/cache", label: "Cache", tier: "expert" },
];

/** How much of a library's own subtree a tier may see (ADR-26, asked for
 *  2026-09-13). Browsing the collection is the normal case; the operational
 *  screens are advanced; the two profile builders are expert. */
const LEVEL_TIERS: Record<string, Tier> = { artists: "normal", albums: "normal", tracks: "advanced" };

/** Libraries sharing a media type are grouped once there is more than one of
 *  that type (ADR-54) -- encounter order preserved, the caller's own
 *  snapshot order (Invariant 36), never re-sorted here. */
function groupByMediaType(libraries: readonly LibraryEntry[]): { mediaType: string; libraries: LibraryEntry[] }[] {
  const groups: { mediaType: string; libraries: LibraryEntry[] }[] = [];
  for (const library of libraries) {
    const group = groups.find((candidate) => candidate.mediaType === library.media_type);
    if (group) group.libraries.push(library);
    else groups.push({ mediaType: library.media_type, libraries: [library] });
  }
  return groups;
}

/** A library's default level -- the caller's own last-visited one
 *  (user_preference, ADR-54) when it still names a level this library
 *  declares, else Artists (the Lidarr landing view), else whatever it
 *  declared first. */
export function defaultLevelKey(library: LibraryEntry): string | undefined {
  const keys = new Set(library.levels.map((level) => level.key));
  if (library.last_level && keys.has(library.last_level)) return library.last_level;
  return library.levels.find((level) => level.key === "artists")?.key ?? library.levels[0]?.key;
}

/** The sidebar tree. Every top-level section and every submenu is
 *  click-to-toggle, several may stand open at once (useOpenSections), and
 *  navigating into a branch opens it without closing the rest. */
export function Sidebar() {
  const tier = useTier();
  const path = useRouterState({ select: (state) => state.location.pathname });
  const routeParams = useParams({ strict: false }) as {
    categoryId?: string;
    level?: string;
    section?: string;
  };
  const search = useSearch({ strict: false }) as { category_id?: string };
  const collapsed = useSidebarCollapsed();

  const libraries = useQuery({
    queryKey: ["libraries"],
    queryFn: async () => {
      const { data, error } = await api.GET("/libraries");
      if (error) throw error;
      return data;
    },
  });

  const groups = groupByMediaType(libraries.data ?? []);

  // Which branches the current route sits inside, so they open themselves.
  const activeKeys: string[] = [];
  if (path.startsWith("/library") || routeParams.categoryId) activeKeys.push("library");
  if (path.startsWith("/storage")) activeKeys.push("storage");
  if (path.startsWith("/connections")) activeKeys.push("connections");
  if (path.startsWith("/settings")) activeKeys.push("settings");
  if (routeParams.categoryId) activeKeys.push(`library:${routeParams.categoryId}`);
  if (path === "/unidentified" && search.category_id) activeKeys.push(`library:${search.category_id}`);

  // Library starts open on a first visit: it is where the collection lives.
  const sections = useOpenSections(activeKeys, ["library"]);

  const libraryTree = groups.map((group) => (
    <li key={group.mediaType}>
      {group.libraries.length > 1 && !collapsed && (
        <div className="px-2 pt-2 pb-0.5 text-[11px] uppercase tracking-wide text-ink-muted">
          {group.mediaType}
        </div>
      )}
      <ul className="flex flex-col gap-0.5">
        {group.libraries.map((library) => (
          <LibraryNode
            key={library.id}
            library={library}
            collapsed={collapsed}
            tier={tier}
            isActive={routeParams.categoryId === String(library.id)}
            activeLevel={routeParams.level}
            activeSection={routeParams.section}
            activePath={path}
            activeUnidentifiedCategoryId={path === "/unidentified" ? search.category_id : undefined}
            isOpen={sections.isOpen(`library:${library.id}`)}
            onToggle={() => sections.toggle(`library:${library.id}`)}
          />
        ))}
      </ul>
    </li>
  ));

  return (
    <nav
      aria-label="Sections"
      className={`flex shrink-0 flex-col overflow-y-auto border-r border-border-subtle bg-surface ${
        collapsed ? "w-14" : "w-48"
      }`}
    >
      <div className={`flex items-center gap-2 px-4 py-3 ${collapsed ? "justify-center" : "justify-between"}`}>
        {!collapsed && <span className="text-sm font-semibold tracking-tight">Solarr</span>}
        <ThemeToggle />
      </div>

      <ul className="flex flex-col gap-0.5 px-2">
        <li>
          <Link to="/" title={collapsed ? "Dashboard" : undefined} className={sectionLinkClass(path === "/", collapsed)}>
            <LayoutDashboard aria-hidden className="size-4 shrink-0" />
            {!collapsed && "Dashboard"}
          </Link>
        </li>
      </ul>

      <CollapsibleSection
        label="Library"
        icon={LibraryIcon}
        to="/libraries"
        collapsed={collapsed}
        active={path === "/libraries"}
        isOpen={sections.isOpen("library")}
        onToggle={() => sections.toggle("library")}
        whenCollapsed={libraryTree}
      >
        {libraryTree}
      </CollapsibleSection>

      <CollapsibleSection
        label="Storage"
        icon={HardDrive}
        to="/storage/roots"
        collapsed={collapsed}
        active={path.startsWith("/storage")}
        isOpen={sections.isOpen("storage")}
        onToggle={() => sections.toggle("storage")}
      >
        {STORAGE_SECTIONS.map(({ to, label, icon: Icon }) => (
          <li key={to}>
            <Link to={to} className={sectionLinkClass(path.startsWith(to), false)}>
              <Icon aria-hidden className="size-4 shrink-0" />
              {label}
            </Link>
          </li>
        ))}
      </CollapsibleSection>

      <CollapsibleSection
        label="Connections"
        icon={Share2}
        to="/connections/indexers"
        collapsed={collapsed}
        active={path.startsWith("/connections")}
        isOpen={sections.isOpen("connections")}
        onToggle={() => sections.toggle("connections")}
      >
        {CONNECTIONS_SECTIONS.map(({ to, label, icon: Icon }) => (
          <li key={to}>
            <Link to={to} className={sectionLinkClass(path.startsWith(to), false)}>
              <Icon aria-hidden className="size-4 shrink-0" />
              {label}
            </Link>
          </li>
        ))}
      </CollapsibleSection>

      <ul className="flex flex-col gap-0.5 px-2 pt-3">
        {SECTIONS.filter((section) => isTierVisible(tier, section.tier)).map(({ to, label, icon: Icon }) => (
          <li key={to}>
            <Link to={to} title={collapsed ? label : undefined} className={sectionLinkClass(path.startsWith(to), collapsed)}>
              <Icon aria-hidden className="size-4 shrink-0" />
              {!collapsed && label}
            </Link>
          </li>
        ))}
      </ul>

      <CollapsibleSection
        label="Settings"
        icon={Settings}
        to="/settings/general"
        collapsed={collapsed}
        active={path.startsWith("/settings")}
        isOpen={sections.isOpen("settings")}
        onToggle={() => sections.toggle("settings")}
      >
        {SETTINGS_SECTIONS.filter((section) => isTierVisible(tier, section.tier ?? "normal")).map(({ to, label }) => (
          <li key={to}>
            <Link to={to} className={sectionLinkClass(path === to, false)}>
              <span className="truncate">{label}</span>
            </Link>
          </li>
        ))}
      </CollapsibleSection>
    </nav>
  );
}

/** A top-level branch: its label navigates, its chevron only expands, so
 *  opening a section never drags the caller off the page they are on. */
function CollapsibleSection({
  label,
  icon: Icon,
  to,
  collapsed,
  active,
  isOpen,
  onToggle,
  whenCollapsed,
  children,
}: {
  label: string;
  icon: LucideIcon;
  to: string;
  collapsed: boolean;
  active: boolean;
  isOpen: boolean;
  onToggle: () => void;
  /** What stays reachable when the sidebar shrinks to icons, if anything. */
  whenCollapsed?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <>
      <ul className="flex flex-col gap-0.5 px-2 pt-3">
        <li className="flex items-center">
          <Link to={to} title={collapsed ? label : undefined} className={sectionLinkClass(active, collapsed) + " flex-1"}>
            <Icon aria-hidden className="size-4 shrink-0" />
            {!collapsed && label}
          </Link>
          {!collapsed && <ExpandToggle label={label} isOpen={isOpen} onToggle={onToggle} />}
        </li>
        {!collapsed && isOpen && (
          <li>
            <ul className="ml-5 flex flex-col gap-0.5 border-l border-border-subtle pl-2">{children}</ul>
          </li>
        )}
        {collapsed && whenCollapsed}
      </ul>
    </>
  );
}

/** The chevron, a real button rather than an ornament inside the link. */
function ExpandToggle({ label, isOpen, onToggle }: { label: string; isOpen: boolean; onToggle: () => void }) {
  return (
    <button
      type="button"
      onClick={onToggle}
      aria-expanded={isOpen}
      aria-label={`${isOpen ? "Collapse" : "Expand"} ${label}`}
      className="rounded-md p-1 text-ink-muted hover:bg-canvas hover:text-ink
                 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
    >
      {isOpen ? (
        <ChevronDown aria-hidden className="size-3.5" />
      ) : (
        <ChevronRight aria-hidden className="size-3.5" />
      )}
    </button>
  );
}

// Sibling to the entity levels, not one of them (ADR-54's own tree sketch):
// generic per-library config, each with the tier that may see it.
const CONFIG_SECTIONS: readonly { key: string; label: string; tier: Tier }[] = [
  { key: "naming", label: "Naming", tier: "advanced" },
  { key: "import-lists", label: "Import lists", tier: "advanced" },
  { key: "quality", label: "Quality profiles", tier: "expert" },
  { key: "language", label: "Language", tier: "expert" },
];

function LibraryNode({
  library,
  collapsed,
  tier,
  isActive,
  activeLevel,
  activeSection,
  activePath,
  activeUnidentifiedCategoryId,
  isOpen,
  onToggle,
}: {
  library: LibraryEntry;
  collapsed: boolean;
  tier: Tier;
  isActive: boolean;
  activeLevel: string | undefined;
  activeSection: string | undefined;
  activePath: string;
  activeUnidentifiedCategoryId: string | undefined;
  isOpen: boolean;
  onToggle: () => void;
}) {
  const target = defaultLevelKey(library);
  const Icon = mediaTypeIcon(library.media_type);
  const categoryId = String(library.id);

  return (
    <li>
      <div className="flex items-center">
        {target ? (
          <Link
            to="/library/$categoryId/$level"
            params={{ categoryId, level: target }}
            title={collapsed ? library.name : undefined}
            className={sectionLinkClass(isActive && !activeLevel, collapsed) + " flex-1"}
          >
            <Icon aria-hidden className="size-4 shrink-0" />
            {!collapsed && <span className="truncate">{library.name}</span>}
          </Link>
        ) : (
          <span className={sectionLinkClass(false, collapsed) + " flex-1 cursor-default"}>
            <Icon aria-hidden className="size-4 shrink-0" />
            {!collapsed && <span className="truncate">{library.name}</span>}
          </span>
        )}
        {!collapsed && <ExpandToggle label={library.name} isOpen={isOpen} onToggle={onToggle} />}
      </div>

      {!collapsed && isOpen && (
        <ul className="ml-5 flex flex-col gap-0.5 border-l border-border-subtle pl-2">
          {library.levels
            .filter((level) => isTierVisible(tier, LEVEL_TIERS[level.key] ?? "normal"))
            .map((level) => (
              <li key={level.key}>
                <Link
                  to="/library/$categoryId/$level"
                  params={{ categoryId, level: level.key }}
                  className={sectionLinkClass(isActive && activeLevel === level.key, false)}
                >
                  <span className="truncate">{level.label}</span>
                  {level.count != null && <span className="ml-auto shrink-0 text-xs text-ink-muted">{level.count}</span>}
                </Link>
              </li>
            ))}
          <li>
            <Link
              to="/library/$categoryId/wanted"
              params={{ categoryId }}
              className={sectionLinkClass(isActive && activePath === `/library/${library.id}/wanted`, false)}
            >
              <span className="truncate">Wanted</span>
            </Link>
          </li>

          {isTierVisible(tier, "advanced") && (
            <>
              <li>
                <Link
                  to="/library/$categoryId/packs"
                  params={{ categoryId }}
                  className={sectionLinkClass(isActive && activePath === `/library/${library.id}/packs`, false)}
                >
                  <span className="truncate">Packs</span>
                </Link>
              </li>
              <li>
                <Link
                  to="/library/$categoryId/import"
                  params={{ categoryId }}
                  className={sectionLinkClass(isActive && activePath === `/library/${library.id}/import`, false)}
                >
                  <span className="truncate">Import</span>
                </Link>
              </li>
              <li>
                <Link
                  to="/library/$categoryId/activity"
                  params={{ categoryId }}
                  className={sectionLinkClass(isActive && activePath === `/library/${library.id}/activity`, false)}
                >
                  <span className="truncate">Activity</span>
                </Link>
              </li>
              <li>
                <Link
                  to="/unidentified"
                  search={{ category_id: categoryId }}
                  className={sectionLinkClass(activeUnidentifiedCategoryId === categoryId, false)}
                >
                  <span className="truncate">Unidentified</span>
                </Link>
              </li>
            </>
          )}

          {CONFIG_SECTIONS.filter((section) => isTierVisible(tier, section.tier)).map((section) =>
            section.key === "import-lists" ? (
              <li key={section.key}>
                <Link
                  to="/library/$categoryId/config/import-lists"
                  params={{ categoryId }}
                  className={sectionLinkClass(
                    isActive && activePath === `/library/${library.id}/config/import-lists`,
                    false,
                  )}
                >
                  <span className="truncate">{section.label}</span>
                </Link>
              </li>
            ) : (
              <li key={section.key}>
                <Link
                  to="/library/$categoryId/config/$section"
                  params={{ categoryId, section: section.key }}
                  className={sectionLinkClass(isActive && activeSection === section.key, false)}
                >
                  <span className="truncate">{section.label}</span>
                </Link>
              </li>
            ),
          )}
        </ul>
      )}
    </li>
  );
}

function sectionLinkClass(active: boolean, collapsed: boolean) {
  return `flex items-center gap-2 rounded-md px-2 py-1.5 text-sm
          focus-visible:outline-2 focus-visible:outline-offset-2
          focus-visible:outline-accent ${collapsed ? "justify-center" : ""} ${
            active
              ? "bg-accent text-accent-ink"
              : "text-ink-muted hover:bg-canvas hover:text-ink"
          }`;
}
