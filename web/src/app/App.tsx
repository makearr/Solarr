import { useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";
import { Jobs } from "./Jobs";
import { Setup } from "./Setup";
import { ThemeToggle } from "./ThemeToggle";

export function App() {
  // This tab only: the status endpoint already reports what is outstanding.
  const [skipped, setSkipped] = useState(false);

  const setup = useQuery({
    queryKey: ["setup"],
    queryFn: async () => {
      const { data, error } = await api.GET("/setup");
      if (error) throw error;
      return data;
    },
  });

  const about = useQuery({
    queryKey: ["about"],
    queryFn: async () => {
      const { data, error } = await api.GET("/about");
      if (error) throw error;
      return data;
    },
  });

  // After every hook, or React renders fewer hooks on some passes than others.
  const outstanding =
    setup.data && (setup.data.needs_administrator || !setup.data.has_root_folder);
  if (!skipped && outstanding) {
    return <Setup onDone={() => setSkipped(true)} />;
  }

  return (
    <div className="flex h-full flex-col">
      <header className="flex shrink-0 items-center justify-between border-b border-border-subtle
                         bg-surface px-4 py-3">
        <h1 className="text-sm font-semibold tracking-tight">MediaNexus</h1>
        <ThemeToggle />
      </header>

      <main className="min-h-0 flex-1">
        <Jobs />
      </main>

      {/* AGPL section 13 requires the source offer to stay reachable. */}
      <footer className="flex shrink-0 items-center gap-3 border-t border-border-subtle
                         bg-surface px-4 py-1.5 text-xs text-ink-muted">
        {about.data ? (
          <>
            <span className="font-mono">
              {about.data.version} · {about.data.commit}
            </span>
            <span>{about.data.license}</span>
            <a
              className="ml-auto text-accent underline underline-offset-2"
              href={about.data.source_url}
            >
              Source
            </a>
          </>
        ) : (
          <span>Connecting…</span>
        )}
      </footer>
    </div>
  );
}
