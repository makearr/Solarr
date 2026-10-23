import { Link, useParams, useRouterState, useSearch } from "@tanstack/react-router";
import {
  Activity,
  ChevronDown,
  ChevronRight,
  HardDrive,
  LayoutDashboard,
  Library as LibraryIcon,
  Settings,
  Share2,
  type LucideIcon,
  CalendarDays,
  UsersRound,
} from "lucide-react";

import { useSidebarCollapsed } from "@/list/useNarrow";
import { mediaTypeIcon } from "@/lib/mediaTypeIcon";
import { useLibraries } from "@/lib/resources";
import { useShowCalendar } from "@/lib/interfacePreferences";
import { useOpenSections } from "@/lib/useOpenSections";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { useTier, type Tier } from "@/lib/useTier";
import type { components } from "@/api/schema";

import { Logo } from "./Logo";
import {
  ACTIVITY, CONNECTIONS, entryTab, isAt, librarySections, SETTINGS, STORAGE, visibleTabs, type Place, type Section,
} from "./sections";

type LibraryEntry = components["schemas"]["Library"];

// One entry per section; its pages are tabs above the page (sections.ts, 2026-10-04).
const GLOBAL_ENTRIES: readonly { section: Section; icon: LucideIcon }[] = [
  { section: ACTIVITY, icon: Activity },
  { section: STORAGE, icon: HardDrive },
  { section: CONNECTIONS, icon: Share2 },
  { section: SETTINGS, icon: Settings },
];

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
  const administrator = useIsAdministrator();
  const path = useRouterState({ select: (state) => state.location.pathname });
  const routeParams = useParams({ strict: false }) as {
    categoryId?: string;
    level?: string;
    section?: string;
  };
  const search = useSearch({ strict: false }) as { category_id?: string };
  const collapsed = useSidebarCollapsed();
  const [showCalendar] = useShowCalendar();

  const libraries = useLibraries();

  const groups = groupByMediaType(libraries.data ?? []);
  const viewer = { tier, administrator };
  const place = { path, categoryId: search.category_id };

  // Which branches the current route sits inside, so they open themselves.
  const activeKeys: string[] = [];
  if (path.startsWith("/library") || routeParams.categoryId) activeKeys.push("library");
  if (routeParams.categoryId) activeKeys.push(`library:${routeParams.categoryId}`);
  if (path === "/unidentified" && search.category_id) activeKeys.push(`library:${search.category_id}`);

  // Library starts open on a first visit: it is where the collection lives.
  const sections = useOpenSections(activeKeys, ["library"]);

  const libraryTree = groups.map((group) => (
    <li key={group.mediaType}>
      <ul className="flex flex-col gap-0.5">
        {group.libraries.map((library) => (
          <LibraryNode
            key={library.id}
            library={library}
            collapsed={collapsed}
            tier={tier}
            administrator={administrator}
            isActive={routeParams.categoryId === String(library.id)}
            activeLevel={routeParams.level}
            place={place}
            isOpen={sections.isOpen(`library:${library.id}`)}
            onToggle={() => sections.toggle(`library:${library.id}`)}
          />
        ))}
      </ul>
    </li>
  ));

  return (
    <nav
      data-region="sidebar"
      aria-label="Sections"
      className={`flex shrink-0 flex-col overflow-y-auto border-r border-border-subtle bg-surface ${
        collapsed ? "w-14" : "w-48"
      }`}
    >
      <div className={`flex items-center py-3 ${collapsed ? "justify-center px-2" : "px-3"}`}>
        <Logo showName={!collapsed} size="lg" />
      </div>

      <ul className="flex flex-col gap-0.5 px-2">
        <li>
          <Link to="/" title={collapsed ? "Dashboard" : undefined} className={sectionLinkClass(path === "/", collapsed)}>
            <LayoutDashboard aria-hidden className="size-4 shrink-0" />
            {!collapsed && "Dashboard"}
          </Link>
        </li>
        {showCalendar && (
          <li>
            <Link to="/calendar" title={collapsed ? "Calendar" : undefined} className={sectionLinkClass(path.startsWith("/calendar"), collapsed)}>
              <CalendarDays aria-hidden className="size-4 shrink-0" />
              {!collapsed && "Calendar"}
            </Link>
          </li>
        )}
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

      <ul className="flex flex-col gap-0.5 px-2 pt-3">
        <li>
          <Link
            to="/people"
            title={collapsed ? "People" : undefined}
            className={sectionLinkClass(path === "/people" || (path.startsWith("/people/") && !routeParams.categoryId), collapsed)}
          >
            <UsersRound aria-hidden className="size-4 shrink-0" />
            {!collapsed && "People"}
          </Link>
        </li>
        {GLOBAL_ENTRIES.map(({ section, icon: Icon }) => {
          const tab = entryTab(section, viewer);
          if (!tab) return null;
          const active = visibleTabs(section, viewer).some((entry) => isAt(entry, place)) && !routeParams.categoryId
            && !(path === "/unidentified" && search.category_id);
          return (
            <li key={section.key}>
              <Link to={tab.path} title={collapsed ? section.label : undefined} className={sectionLinkClass(active, collapsed)}>
                <Icon aria-hidden className="size-4 shrink-0" />
                {!collapsed && section.label}
              </Link>
            </li>
          );
        })}
      </ul>
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

function LibraryNode({
  library,
  collapsed,
  tier,
  administrator,
  isActive,
  activeLevel,
  place,
  isOpen,
  onToggle,
}: {
  library: LibraryEntry;
  collapsed: boolean;
  tier: Tier;
  administrator: boolean;
  isActive: boolean;
  activeLevel: string | undefined;
  place: Place;
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
          {library.levels.map((level) => {
            const here = isActive && activeLevel === level.key;
            return (
              <li key={level.key}>
                <Link
                  to="/library/$categoryId/$level"
                  params={{ categoryId, level: level.key }}
                  className={sectionLinkClass(here, false)}
                >
                  <span className="truncate" title={level.label}>{level.label}</span>
                  {level.count != null && (
                    <span className={`ml-auto shrink-0 text-xs tabular-nums ${here ? "text-accent-ink/80" : "text-ink-muted"}`}>
                      {level.count.toLocaleString()}
                    </span>
                  )}
                </Link>
              </li>
            );
          })}
          {/* After its own levels every library has the same four entries, each with tabs (2026-10-04). */}
          {librarySections(categoryId).map((section) => {
            const tab = entryTab(section, { tier, administrator });
            if (!tab) return null;
            const active = visibleTabs(section, { tier, administrator }).some((entry) => isAt(entry, place));
            return (
              <li key={section.key}>
                <Link to={tab.path} search={tab.search} className={sectionLinkClass(active, false)}>
                  <span className="truncate">{section.label}</span>
                </Link>
              </li>
            );
          })}
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
