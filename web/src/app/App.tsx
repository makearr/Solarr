import { useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";
import { Jobs } from "./Jobs";
import { Setup } from "./Setup";
import { ThemeToggle } from "./ThemeToggle";

export function App() {
  // Skipping is remembered for this tab only. The wizard is a convenience, not
  // a gate, and persisting "I skipped it" would need a setting that exists to
  // record an absence -- while the status endpoint already says what is
  // outstanding, truthfully, on every load.
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

  // Every hook above runs unconditionally. Returning the wizard before the
  // second useQuery would call fewer hooks on some renders than on others,
  // which React reports as "rendered fewer hooks than expected" -- so the
  // branch happens here, after the hooks, not among them.
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

      {/* The source offer stays reachable from the interface itself, which is
          what AGPL section 13 requires of a program used over a network. */}
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
