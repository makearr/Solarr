import { useState } from "react";

import { Link, useParams, useRouterState, useSearch } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  ArrowLeftRight,
  Boxes,
  ChevronDown,
  ChevronRight,
  Download,
  HardDrive,
  HelpCircle,
  LayoutDashboard,
  Library as LibraryIcon,
  ScanSearch,
  Settings,
  type LucideIcon,
} from "lucide-react";

import { api } from "@/api/client";
import { useSidebarCollapsed } from "@/list/useNarrow";
import type { components } from "@/api/schema";

import { ThemeToggle } from "./ThemeToggle";

type LibraryEntry = components["schemas"]["Library"];

// People stopped being a top-level section 2026-09-09 (ADR-54's reversal):
// a person now surfaces under their own library (Music -> Artists), and
// global search still spans every category, which is where cross-media
// discovery actually happened -- browsing every person was never how anyone
// found one.
const SECTIONS: readonly { to: string; label: string; icon: LucideIcon }[] = [
  { to: "/unidentified", label: "Unidentified", icon: HelpCircle },
  { to: "/activity", label: "Activity", icon: Activity },
  { to: "/settings", label: "Settings", icon: Settings },
];

// Storage (todo 4.18): Import and Move & drain retire from the flat SECTIONS
// list above in favour of this group. Root folders is new (the overview
// screen that never existed); Download folders lands as its own follow-up
// commit right after this one.
const STORAGE_SECTIONS: readonly { to: string; label: string; icon: LucideIcon }[] = [
  { to: "/storage/roots", label: "Root folders", icon: HardDrive },
  { to: "/storage/moves", label: "Move & drain", icon: ArrowLeftRight },
  { to: "/storage/downloads", label: "Download folders", icon: Download },
  { to: "/storage/import", label: "Import", icon: ScanSearch },
  { to: "/storage/packs", label: "Packs", icon: Boxes },
];

const EXPANDED_STORAGE_KEY = "medianexus.sidebar.expandedLibraries";

function loadExpanded(): Set<number> {
  try {
    const raw = localStorage.getItem(EXPANDED_STORAGE_KEY);
    if (!raw) return new Set();
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed) ? new Set(parsed.filter((id) => typeof id === "number")) : new Set();
  } catch {
    return new Set();
  }
}

function saveExpanded(ids: ReadonlySet<number>) {
  try {
    localStorage.setItem(EXPANDED_STORAGE_KEY, JSON.stringify([...ids]));
  } catch {
    // A per-browser convenience; losing it costs nothing worth failing over.
  }
}

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
 *  declares, else Albums, else whatever it declared first. Core knows
 *  nothing about "albums" being special (ADR-23); this is purely a
 *  sidebar-navigation convenience. */
export function defaultLevelKey(library: LibraryEntry): string | undefined {
  const keys = new Set(library.levels.map((level) => level.key));
  if (library.last_level && keys.has(library.last_level)) return library.last_level;
  return library.levels.find((level) => level.key === "albums")?.key ?? library.levels[0]?.key;
}

export function Sidebar() {
  const path = useRouterState({ select: (state) => state.location.pathname });
  const routeParams = useParams({ strict: false }) as {
    categoryId?: string;
    level?: string;
    section?: string;
  };
  // Only "/unidentified"'s own category_id filter is read here -- the queue
  // stays cross-library (todo 4.16), this just tells a per-library link how
  // to render itself active when the caller followed it here.
  const search = useSearch({ strict: false }) as { category_id?: string };
  // Section 21.8's 700-1100px row: icons only, no decision panel to make room
  // for yet (that lands with a persisted Decision, stage 4+).
  const collapsed = useSidebarCollapsed();
  const [expanded, setExpanded] = useState<Set<number>>(loadExpanded);

  const libraries = useQuery({
    queryKey: ["libraries"],
    queryFn: async () => {
      const { data, error } = await api.GET("/libraries");
      if (error) throw error;
      return data;
    },
  });

  const toggle = (id: number) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      saveExpanded(next);
      return next;
    });
  };

  const groups = groupByMediaType(libraries.data ?? []);

  return (
    <nav
      aria-label="Sections"
      className={`flex shrink-0 flex-col border-r border-border-subtle bg-surface ${
        collapsed ? "w-14" : "w-48"
      }`}
    >
      <div className={`flex items-center gap-2 px-4 py-3 ${collapsed ? "justify-center" : "justify-between"}`}>
        {!collapsed && <span className="text-sm font-semibold tracking-tight">MediaNexus</span>}
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

      {groups.length > 0 && (
        <>
          {!collapsed && (
            <div className="px-4 pt-4 pb-1 text-xs font-medium uppercase tracking-wide text-ink-muted">
              Library
            </div>
          )}
          <ul className="flex flex-col gap-0.5 px-2">
            {groups.map((group) => (
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
                      isActive={routeParams.categoryId === String(library.id)}
                      activeLevel={routeParams.level}
                      activeSection={routeParams.section}
                      activePath={path}
                      activeUnidentifiedCategoryId={path === "/unidentified" ? search.category_id : undefined}
                      isExpanded={collapsed ? false : expanded.has(library.id) || routeParams.categoryId === String(library.id)}
                      onToggle={() => toggle(library.id)}
                    />
                  ))}
                </ul>
              </li>
            ))}
          </ul>
        </>
      )}

      {!collapsed && (
        <div className="px-4 pt-4 pb-1 text-xs font-medium uppercase tracking-wide text-ink-muted">
          Storage
        </div>
      )}
      <ul className={`flex flex-col gap-0.5 px-2 ${collapsed ? "pt-4" : ""}`}>
        {STORAGE_SECTIONS.map(({ to, label, icon: Icon }) => {
          const active = path.startsWith(to);
          return (
            <li key={to}>
              <Link to={to} title={collapsed ? label : undefined} className={sectionLinkClass(active, collapsed)}>
                <Icon aria-hidden className="size-4 shrink-0" />
                {!collapsed && label}
              </Link>
            </li>
          );
        })}
      </ul>

      <ul className="flex flex-col gap-0.5 px-2 pt-4">
        {SECTIONS.map(({ to, label, icon: Icon }) => {
          const active = path.startsWith(to);
          return (
            <li key={to}>
              <Link to={to} title={collapsed ? label : undefined} className={sectionLinkClass(active, collapsed)}>
                <Icon aria-hidden className="size-4 shrink-0" />
                {!collapsed && label}
              </Link>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}

// Sibling to the entity levels, not one of them (ADR-54's own tree sketch):
// generic per-library config, not a media-type-declared NavigationLevel, so
// it is never in library.levels and gets its own route rather than reusing
// /library/$categoryId/$level.
const CONFIG_SECTIONS: readonly { key: string; label: string }[] = [
  { key: "quality", label: "Quality profiles" },
  { key: "naming", label: "Naming" },
  { key: "language", label: "Language" },
  { key: "import-lists", label: "Import lists" },
];

function LibraryNode({
  library,
  collapsed,
  isActive,
  activeLevel,
  activeSection,
  activePath,
  activeUnidentifiedCategoryId,
  isExpanded,
  onToggle,
}: {
  library: LibraryEntry;
  collapsed: boolean;
  isActive: boolean;
  activeLevel: string | undefined;
  activeSection: string | undefined;
  activePath: string;
  activeUnidentifiedCategoryId: string | undefined;
  isExpanded: boolean;
  onToggle: () => void;
}) {
  const target = defaultLevelKey(library);

  return (
    <li>
      <div className="flex items-center">
        {!collapsed && (
          <button
            type="button"
            aria-label={isExpanded ? `Collapse ${library.name}` : `Expand ${library.name}`}
            onClick={onToggle}
            className="p-1 text-ink-muted hover:text-ink focus-visible:outline-2
                       focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            {isExpanded ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
          </button>
        )}
        {target ? (
          <Link
            to="/library/$categoryId/$level"
            params={{ categoryId: String(library.id), level: target }}
            title={collapsed ? library.name : undefined}
            className={sectionLinkClass(isActive && !activeLevel, collapsed) + " flex-1"}
          >
            <LibraryIcon aria-hidden className="size-4 shrink-0" />
            {!collapsed && <span className="truncate">{library.name}</span>}
          </Link>
        ) : (
          <span className={sectionLinkClass(false, collapsed) + " flex-1 cursor-default"}>
            <LibraryIcon aria-hidden className="size-4 shrink-0" />
            {!collapsed && <span className="truncate">{library.name}</span>}
          </span>
        )}
      </div>

      {!collapsed && isExpanded && (library.levels.length > 0 || CONFIG_SECTIONS.length > 0) && (
        <ul className="ml-5 flex flex-col gap-0.5 border-l border-border-subtle pl-2">
          {library.levels.map((level) => (
            <li key={level.key}>
              <Link
                to="/library/$categoryId/$level"
                params={{ categoryId: String(library.id), level: level.key }}
                className={sectionLinkClass(isActive && activeLevel === level.key, false)}
              >
                <span className="truncate">{level.label}</span>
                {level.count != null && <span className="ml-auto shrink-0 text-xs text-ink-muted">{level.count}</span>}
              </Link>
            </li>
          ))}
          <li>
            <Link
              to="/unidentified"
              search={{ category_id: String(library.id) }}
              className={sectionLinkClass(activeUnidentifiedCategoryId === String(library.id), false)}
            >
              <span className="truncate">Unidentified</span>
            </Link>
          </li>
          <li>
            <Link
              to="/library/$categoryId/discover"
              params={{ categoryId: String(library.id) }}
              className={sectionLinkClass(isActive && activePath === `/library/${library.id}/discover`, false)}
            >
              <span className="truncate">Discover</span>
            </Link>
          </li>
          {CONFIG_SECTIONS.map((section) =>
            section.key === "import-lists" ? (
              <li key={section.key}>
                <Link
                  to="/library/$categoryId/config/import-lists"
                  params={{ categoryId: String(library.id) }}
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
                  params={{ categoryId: String(library.id), section: section.key }}
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
