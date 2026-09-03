import { SearchBox } from "./SearchBox";
import { Sidebar } from "./Sidebar";
import { StatusBar } from "./StatusBar";

/** Sidebar, header, content, status bar. The setup gate sits outside this in App, so a
 *  half-configured install is never shown a dashboard of zeroes it cannot act
 *  on. */
export function Shell({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-full flex-col">
      <div className="flex min-h-0 flex-1">
        <Sidebar />
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <header className="flex shrink-0 items-center justify-end border-b border-border-subtle px-4 py-2">
            <SearchBox />
          </header>
          <main className="min-h-0 flex-1">{children}</main>
        </div>
      </div>
      <StatusBar />
    </div>
  );
}
