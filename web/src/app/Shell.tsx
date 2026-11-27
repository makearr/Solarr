import { useEffect, useState } from "react";
import { useLocation } from "@tanstack/react-router";
import { Menu, Search, X } from "lucide-react";

import { setOverlay } from "@/lib/overlays";
import { useApplyDensity } from "@/lib/density";
import { t } from "@/lib/i18n";
import { useNarrow } from "@/list/useNarrow";
import { useShowFooter } from "@/lib/interfacePreferences";
import { isAdministratorPath, useIsAdministrator } from "@/lib/useAdministrator";

import { Toaster } from "@/ui/Toaster";

import { ErrorBoundary } from "./ErrorBoundary";
import { NotificationBell } from "./Notifications";
import { PlayerProvider } from "./player/PlayerContext";
import { PlayerDock } from "./player/PlayerDock";
import { CommandPalette } from "./CommandPalette";
import { GlobalShortcuts } from "./GlobalShortcuts";
import { ShortcutsHelp } from "./ShortcutsHelp";
import { Sidebar } from "./Sidebar";
import { StatusBar } from "./StatusBar";
import { SectionTabs } from "./SectionTabs";
import { LibraryTabs } from "./LibraryTabs";
import { UserMenu } from "./UserMenu";

/** Sidebar, header, content, status bar. The setup gate sits outside this in App, so a
 *  half-configured install is never shown a dashboard of zeroes it cannot act
 *  on. */
export function Shell({ children }: { children: React.ReactNode }) {
  const [showFooter] = useShowFooter();
  const narrow = useNarrow();
  useApplyDensity();
  const [drawerOpen, setDrawerOpen] = useState(false);
  const path = useLocation({ select: (location) => location.pathname });
  // Following any link closes the drawer; so does leaving the narrow layout.
  useEffect(() => setDrawerOpen(false), [path, narrow]);
  return (
    <PlayerProvider>
    <div className="flex h-full flex-col">
      <div className="flex min-h-0 flex-1">
        {!narrow && <Sidebar />}
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <header
            data-region="header"
            className="flex shrink-0 items-center justify-end gap-3 border-b border-border-subtle bg-header px-4 py-2"
          >
            {narrow && (
              <button
                type="button"
                onClick={() => setDrawerOpen(true)}
                aria-label={t("nav.openMenu")}
                className="mr-auto rounded-md p-1.5 text-ink-muted hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
              >
                <Menu aria-hidden className="size-5" />
              </button>
            )}
            <button
              type="button"
              onClick={() => setOverlay("palette")}
              aria-keyshortcuts="Control+K /"
              className="flex w-72 items-center gap-2 rounded-md border border-border-subtle bg-canvas px-2.5 py-1.5 text-left text-sm text-ink-muted
                         hover:border-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              <Search aria-hidden className="size-3.5 shrink-0" />
              <span className="min-w-0 flex-1 truncate">{t("palette.trigger")}</span>
              <kbd aria-hidden className="rounded border border-border-subtle px-1.5 text-[0.6875rem] leading-4">Ctrl K</kbd>
            </button>
            <NotificationBell />
            <UserMenu />
          </header>
          <main data-region="content" className="min-h-0 flex-1 overflow-y-auto">
            <LibraryTabs />
            <SectionTabs />
            <AdministratorGate>{children}</AdministratorGate>
          </main>
        </div>
      </div>
      {narrow && drawerOpen && (
        <div className="fixed inset-0 z-40 flex">
          <Sidebar expanded />
          <button
            type="button"
            aria-label={t("nav.closeMenu")}
            onClick={() => setDrawerOpen(false)}
            className="flex-1 bg-black/50 p-3 text-left text-white"
          >
            <X aria-hidden className="size-5" />
          </button>
        </div>
      )}
      <Toaster />
      <GlobalShortcuts />
      <CommandPalette />
      <ShortcutsHelp />
      <ErrorBoundary name="The player"><PlayerDock /></ErrorBoundary>
      {showFooter && <ErrorBoundary name="The status bar"><StatusBar /></ErrorBoundary>}
    </div>
    </PlayerProvider>
  );
}

/** A typed or bookmarked URL to a configuration page says why it is empty rather than showing
 *  controls the server refuses. */
function AdministratorGate({ children }: { children: React.ReactNode }) {
  const path = useLocation({ select: (location) => location.pathname });
  const administrator = useIsAdministrator();
  if (administrator || !isAdministratorPath(path)) return <>{children}</>;
  return (
    <p className="p-6 text-sm text-ink-muted">{t("nav.administratorOnly")}</p>
  );
}
