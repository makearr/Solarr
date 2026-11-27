import { useQuery } from "@tanstack/react-query";
import { Link, useParams, useRouterState, useSearch } from "@tanstack/react-router";
import {
  Activity,
  ChevronDown,
  ChevronRight,
  LayoutDashboard,
  Plus,
  Settings,
  CalendarDays,
  Compass,
  Cable,
  type LucideIcon,
} from "lucide-react";

import { api, ok } from "@/api/client";
import { t } from "@/lib/i18n";
import { useSidebarCollapsed } from "@/list/useNarrow";
import { mediaTypeIcon } from "@/lib/mediaTypeIcon";
import { useDiscoverLibraries, useLibraryTree } from "@/lib/resources";
import { useShowCollections } from "@/lib/useShowCollections";
import { useMediaTypes } from "@/lib/useMediaTypes";
import { useShowCalendar } from "@/lib/interfacePreferences";
import { useOpenSections } from "@/lib/useOpenSections";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { isTierVisible, useTier } from "@/lib/useTier";
import type { components } from "@/api/schema";

import { Logo } from "./Logo";
import { queueQuery } from "./Queue";
import {
  ACTIVITY, CONNECTIONS, entryTab, isAt, librarySections, SETTINGS, SETTINGS_GROUPS, visibleTabs, type Place, type SectionTab, type Viewer,
} from "./sections";

type LibraryEntry = components["schemas"]["Library"];

/** A library's default level -- the caller's own last-visited one
 *  (user_preference, ADR-54) when it still names a level this library
 *  declares, else Artists (the Lidarr landing view), else whatever it
 *  declared first. */
export function defaultLevelKey(library: LibraryEntry): string | undefined {
  if (library.members?.length) return "all";
  const keys = new Set(library.levels.map((level) => level.key));
  if (library.last_level && keys.has(library.last_level)) return library.last_level;
  return library.levels.find((level) => level.key === "artists")?.key ?? library.levels[0]?.key;
}

/** The sidebar's badges: only what asks the viewer to act, from queries other screens already keep warm. */
function useNavBadges(viewer: Viewer) {
  const queue = useQuery({ ...queueQuery(), refetchInterval: 15_000 });
  const health = useQuery({
    queryKey: ["system-health"],
    queryFn: () => ok(api.GET("/system/health")),
    refetchInterval: 60_000,
    enabled: viewer.administrator,
  });
  const unidentified = useQuery({
    queryKey: ["unidentified", "badge"],
    queryFn: () => ok(api.GET("/library/unidentified", { params: { query: { limit: 50 } } })),
    refetchInterval: 60_000,
    enabled: isTierVisible(viewer.tier, "advanced"),
  });
  return {
    downloads: queue.data?.items.length ?? 0,
    health: viewer.administrator ? (health.data?.length ?? 0) : 0,
    unidentified: unidentified.data ? `${unidentified.data.items.length}${unidentified.data.next_cursor ? "+" : ""}` : "",
  };
}

function Badge({ children, active, tone = "accent" }: { children: React.ReactNode; active?: boolean; tone?: "accent" | "warn" }) {
  const colour = active ? "bg-accent-ink/20 text-accent-ink" : tone === "warn" ? "bg-warn/20 text-warn" : "bg-accent/15 text-accent";
  return <span className={`ml-auto shrink-0 rounded-full px-1.5 text-[0.6875rem] font-medium tabular-nums ${colour}`}>{children}</span>;
}

function Heading({ children, collapsed }: { children: React.ReactNode; collapsed: boolean }) {
  if (collapsed) return <div aria-hidden className="mx-3 mt-3 border-t border-border-subtle" />;
  return <h2 className="px-4 pb-1 pt-4 text-[0.6875rem] font-medium uppercase tracking-wide text-ink-muted">{children}</h2>;
}

/** The sidebar: Browse, Libraries, Manage. `expanded` forces the full width (the off-canvas drawer);
 *  otherwise it shrinks to an icon rail on medium screens. */
export function Sidebar({ expanded = false }: { expanded?: boolean }) {
  const tier = useTier();
  const administrator = useIsAdministrator();
  const path = useRouterState({ select: (state) => state.location.pathname });
  const routeParams = useParams({ strict: false }) as { categoryId?: string; level?: string };
  const search = useSearch({ strict: false }) as { category_id?: string };
  const railWidth = useSidebarCollapsed();
  const collapsed = railWidth && !expanded;
  const [showCalendar] = useShowCalendar();
  const discoverLibraries = useDiscoverLibraries();
  const libraries = useLibraryTree();

  const viewer = { tier, administrator };
  const place = { path, categoryId: search.category_id };
  const badges = useNavBadges(viewer);
  const settingsTabs = visibleTabs(SETTINGS, viewer);
  const onLibraryConfig = /^\/library\/[^/]+\/config(\/|$)/.test(path);
  const inSettings = onLibraryConfig
    || (!routeParams.categoryId && !(path === "/unidentified" && search.category_id) && settingsTabs.some((entry) => isAt(entry, place)));

  // Which branches the current route sits inside, so they open themselves.
  const activeKeys: string[] = [];
  const openLibrary = (libraries.data ?? []).find((library) =>
    [library, ...(library.members ?? [])].some((entry) => String(entry.id) === routeParams.categoryId),
  );
  if (openLibrary && !onLibraryConfig) activeKeys.push(`library:${openLibrary.id}`);
  if (path === "/unidentified" && search.category_id) activeKeys.push(`library:${search.category_id}`);
  if (inSettings) {
    activeKeys.push("settings");
    const group = settingsTabs.find((entry) => isAt(entry, place))?.group ?? (onLibraryConfig ? "Media" : undefined);
    if (group) activeKeys.push(`settings:${group}`);
  }
  const sections = useOpenSections(activeKeys);

  const activityTab = entryTab(ACTIVITY, viewer);
  const connectionsTab = entryTab(CONNECTIONS, viewer);
  const settingsEntry = entryTab(SETTINGS, viewer);

  return (
    <nav
      data-region="sidebar"
      aria-label="Sections"
      className={`flex shrink-0 flex-col overflow-y-auto border-r border-border-subtle bg-surface ${
        expanded ? "h-full w-64" : collapsed ? "w-14" : "w-52"
      }`}
    >
      <div className={`flex items-center py-3 ${collapsed ? "justify-center px-2" : "px-3"}`}>
        <Logo showName={!collapsed} size="lg" />
      </div>

      <Heading collapsed={collapsed}>{t("nav.browse")}</Heading>
      <ul className="flex flex-col gap-0.5 px-2">
        <NavLink to="/" icon={LayoutDashboard} label={t("nav.dashboard")} collapsed={collapsed} active={path === "/"} />
        {discoverLibraries.data && discoverLibraries.data.length > 0 && (
          <NavLink to="/discover" icon={Compass} label={t("discover.page")} collapsed={collapsed} active={path.startsWith("/discover")} />
        )}
        {showCalendar && (
          <NavLink to="/calendar" icon={CalendarDays} label={t("nav.calendar")} collapsed={collapsed} active={path.startsWith("/calendar")} />
        )}
      </ul>

      {(administrator || (libraries.data?.length ?? 0) > 0) && <Heading collapsed={collapsed}>{t("nav.libraries")}</Heading>}
      <ul className="flex flex-col gap-0.5 px-2">
        {(libraries.data ?? []).map((library) => (
          <LibraryNode
            key={library.id}
            library={library}
            collapsed={collapsed}
            activeId={!onLibraryConfig && [library, ...(library.members ?? [])].some((entry) => String(entry.id) === routeParams.categoryId) ? routeParams.categoryId : undefined}
            activeLevel={routeParams.level}
            isOpen={sections.isOpen(`library:${library.id}`)}
            onToggle={() => sections.toggle(`library:${library.id}`)}
          />
        ))}
        {administrator && (
          <NavLink to="/settings/libraries" icon={Plus} label={t("nav.addLibrary")} collapsed={collapsed} active={false} />
        )}
      </ul>

      <Heading collapsed={collapsed}>{t("nav.manage")}</Heading>
      <ul className="flex flex-col gap-0.5 px-2 pb-3">
        {activityTab && (
          <NavLink
            to={activityTab.path}
            icon={Activity}
            label={ACTIVITY.label}
            collapsed={collapsed}
            active={visibleTabs(ACTIVITY, viewer).some((entry) => isAt(entry, place)) && !routeParams.categoryId}
            badge={badges.downloads > 0 ? <Badge>{badges.downloads}</Badge> : undefined}
          />
        )}
        {connectionsTab && (
          <NavLink
            to={connectionsTab.path}
            icon={Cable}
            label={t("nav.connections")}
            collapsed={collapsed}
            active={visibleTabs(CONNECTIONS, viewer).some((entry) => isAt(entry, place)) && !routeParams.categoryId}
          />
        )}
        {settingsEntry && collapsed && (
          <NavLink to={settingsEntry.path} icon={Settings} label={SETTINGS.label} collapsed active={inSettings} />
        )}
        {settingsEntry && !collapsed && (
          <li>
            <div className="flex items-center">
              <Link to={settingsEntry.path} className={sectionLinkClass(inSettings, false) + " flex-1"}>
                <Settings aria-hidden className="size-4 shrink-0" />
                {SETTINGS.label}
                {badges.health > 0 && <Badge tone="warn" active={inSettings}>{badges.health}</Badge>}
              </Link>
              <ExpandToggle label={SETTINGS.label} isOpen={sections.isOpen("settings")} onToggle={() => sections.toggle("settings")} />
            </div>
            {sections.isOpen("settings") && (
              <ul className="ml-3 flex flex-col gap-0.5 border-l border-border-subtle pl-1">
                {SETTINGS_GROUPS.map((group) => {
                  const tabs = settingsTabs.filter((entry) => entry.group === group);
                  if (tabs.length === 0) return null;
                  const key = `settings:${group}`;
                  return (
                    <li key={group}>
                      <button
                        type="button"
                        onClick={() => sections.toggle(key)}
                        aria-expanded={sections.isOpen(key)}
                        className="flex w-full items-center gap-1 rounded-md px-2 py-1 text-[0.6875rem] font-medium uppercase tracking-wide
                                   text-ink-muted hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
                      >
                        {sections.isOpen(key) ? <ChevronDown aria-hidden className="size-3" /> : <ChevronRight aria-hidden className="size-3" />}
                        {group}
                      </button>
                      {sections.isOpen(key) && (
                        <ul className="flex flex-col gap-0.5">
                          {tabs.map((entry) => (
                            <SettingsEntry
                              key={entry.label}
                              entry={entry}
                              place={place}
                              viewer={viewer}
                              onLibraryConfig={onLibraryConfig}
                              configLibraryId={onLibraryConfig ? routeParams.categoryId : undefined}
                              libraries={libraries.data ?? []}
                              unidentified={badges.unidentified}
                            />
                          ))}
                        </ul>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </li>
        )}
      </ul>
    </nav>
  );
}

/** One page of Settings; Libraries also lists each library, whose own settings live under it. */
function SettingsEntry({
  entry, place, viewer, onLibraryConfig, configLibraryId, libraries, unidentified,
}: {
  entry: SectionTab;
  place: Place;
  viewer: Viewer;
  onLibraryConfig: boolean;
  configLibraryId: string | undefined;
  libraries: readonly LibraryEntry[];
  unidentified: string;
}) {
  const active = isAt(entry, place) && !onLibraryConfig;
  const isLibraries = entry.path === "/settings/libraries";
  const heads = isLibraries && isTierVisible(viewer.tier, "advanced") ? libraries : [];
  return (
    <li>
      <Link to={entry.path} className={sectionLinkClass(active, false)}>
        <span className="truncate">{entry.label}</span>
        {entry.label === "Unidentified" && unidentified !== "" && unidentified !== "0" && <Badge active={active}>{unidentified}</Badge>}
      </Link>
      {heads.length > 0 && (
        <ul className="ml-3 flex flex-col gap-0.5 border-l border-border-subtle pl-1">
          {heads.map((library) => {
            const configEntry = entryTab(librarySections(String(library.id)).find((section) => section.key.endsWith(":settings"))!, viewer);
            if (!configEntry) return null;
            return (
              <li key={library.id}>
                <Link to={configEntry.path} className={sectionLinkClass(onLibraryConfig && configLibraryId === String(library.id), false)}>
                  <span className="truncate">{library.name}</span>
                </Link>
              </li>
            );
          })}
        </ul>
      )}
    </li>
  );
}

function NavLink({ to, icon: Icon, label, collapsed, active, badge }: {
  to: string;
  icon: LucideIcon;
  label: string;
  collapsed: boolean;
  active: boolean;
  badge?: React.ReactNode;
}) {
  return (
    <li>
      <Link to={to} title={collapsed ? label : undefined} aria-label={collapsed ? label : undefined} className={sectionLinkClass(active, collapsed)}>
        <Icon aria-hidden className="size-4 shrink-0" />
        {!collapsed && label}
        {!collapsed && badge}
      </Link>
    </li>
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
  activeId,
  activeLevel,
  isOpen,
  onToggle,
}: {
  library: LibraryEntry;
  collapsed: boolean;
  activeId: string | undefined;
  activeLevel: string | undefined;
  isOpen: boolean;
  onToggle: () => void;
}) {
  const mediaTypes = useMediaTypes();
  const members = library.members ?? [];
  const target = defaultLevelKey(library);
  const Icon = mediaTypeIcon(library.media_type);
  const categoryId = String(library.id);
  const isActive = activeId !== undefined;
  const showCollections = useShowCollections();
  // A mixed library's levels: its own, then each further type's, the type named where a label repeats (L4).
  const ownLabels = new Set(library.levels.map((level) => level.label));
  const entries = [
    ...(members.length > 0 ? [{ id: categoryId, key: "all", label: "All" }] : []),
    ...[library, ...members].flatMap((entry, index) =>
      entry.levels.filter((level) => showCollections || level.key !== "collections").map((level) => {
        const typeName = mediaTypes.find((info) => info.key === entry.media_type)?.work.many ?? entry.media_type;
        const label = index > 0 && ownLabels.has(level.label) ? `${level.label} · ${typeName}` : level.label;
        return { id: String(entry.id), key: level.key, label };
      }),
    ),
  ];

  return (
    <li>
      <div className="flex items-center">
        {target ? (
          <Link
            to="/library/$categoryId/$level"
            params={{ categoryId, level: target }}
            title={collapsed ? library.name : undefined}
            aria-label={collapsed ? library.name : undefined}
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
          {entries.map((level) => {
            const here = activeId === level.id && activeLevel === level.key;
            return (
              <li key={`${level.id}:${level.key}`}>
                <Link
                  to="/library/$categoryId/$level"
                  params={{ categoryId: level.id, level: level.key }}
                  className={sectionLinkClass(here, false)}
                >
                  <span className="truncate" title={level.label}>{level.label}</span>
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
