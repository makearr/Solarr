import { useState } from "react";

import { Link, useParams, useRouterState } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  ArrowLeftRight,
  ChevronDown,
  ChevronRight,
  HelpCircle,
  LayoutDashboard,
  Library as LibraryIcon,
  ScanSearch,
  Settings,
  Users,
  type LucideIcon,
} from "lucide-react";

import { api } from "@/api/client";
import { useSidebarCollapsed } from "@/list/useNarrow";
import type { components } from "@/api/schema";

import { ThemeToggle } from "./ThemeToggle";

type LibraryEntry = components["schemas"]["Library"];

/** Fixed sections: not categories, so not driven by /libraries (section 21.1). */
const SECTIONS: readonly { to: string; label: string; icon: LucideIcon }[] = [
  { to: "/people", label: "People", icon: Users },
  { to: "/unidentified", label: "Unidentified", icon: HelpCircle },
  { to: "/import", label: "Import", icon: ScanSearch },
  { to: "/moves", label: "Move & drain", icon: ArrowLeftRight },
  { to: "/activity", label: "Activity", icon: Activity },
  { to: "/settings", label: "Settings", icon: Settings },
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

/** A library's default level -- Albums when it declares one, otherwise
 *  whatever it declared first. Core knows nothing about "albums" being
 *  special (ADR-23); this is purely a sidebar-navigation convenience. */
function defaultLevelKey(library: LibraryEntry): string | undefined {
  return library.levels.find((level) => level.key === "albums")?.key ?? library.levels[0]?.key;
}

export function Sidebar() {
  const path = useRouterState({ select: (state) => state.location.pathname });
  const routeParams = useParams({ strict: false }) as { categoryId?: string; level?: string };
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

function LibraryNode({
  library,
  collapsed,
  isActive,
  activeLevel,
  isExpanded,
  onToggle,
}: {
  library: LibraryEntry;
  collapsed: boolean;
  isActive: boolean;
  activeLevel: string | undefined;
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

      {!collapsed && isExpanded && library.levels.length > 0 && (
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
