import { Sidebar } from "./Sidebar";
import { StatusBar } from "./StatusBar";

/** Sidebar, content, status bar. The setup gate sits outside this in App, so a
 *  half-configured install is never shown a dashboard of zeroes it cannot act
 *  on. */
export function Shell({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-full flex-col">
      <div className="flex min-h-0 flex-1">
        <Sidebar />
        <main className="min-w-0 flex-1">{children}</main>
      </div>
      <StatusBar />
    </div>
  );
}
