import { useLocation } from "@tanstack/react-router";

import { isAdministratorPath, useIsAdministrator } from "@/lib/useAdministrator";

import { SearchBox } from "./SearchBox";
import { Sidebar } from "./Sidebar";
import { StatusBar } from "./StatusBar";
import { ThemeToggle } from "./ThemeToggle";
import { UserMenu } from "./UserMenu";

/** Sidebar, header, content, status bar. The setup gate sits outside this in App, so a
 *  half-configured install is never shown a dashboard of zeroes it cannot act
 *  on. */
export function Shell({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-full flex-col">
      <div className="flex min-h-0 flex-1">
        <Sidebar />
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <header className="flex shrink-0 items-center justify-end gap-3 border-b border-border-subtle px-4 py-2">
            <SearchBox />
            <ThemeToggle />
            <UserMenu />
          </header>
          <main className="min-h-0 flex-1">
            <AdministratorGate>{children}</AdministratorGate>
          </main>
        </div>
      </div>
      <StatusBar />
    </div>
  );
}

/** A typed or bookmarked URL to a configuration page says why it is empty rather than showing
 *  controls the server refuses. */
function AdministratorGate({ children }: { children: React.ReactNode }) {
  const path = useLocation({ select: (location) => location.pathname });
  const administrator = useIsAdministrator();
  if (administrator || !isAdministratorPath(path)) return <>{children}</>;
  return (
    <p className="p-6 text-sm text-ink-muted">
      Only an administrator can change this. Your own account is under Settings &rarr; User.
    </p>
  );
}
