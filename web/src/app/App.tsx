import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";
import { ThemeToggle } from "./ThemeToggle";

// The shell is deliberately thin. It proves the three things this stage is
// about -- the bundle is served, the generated client reaches the API, and the
// theme applies -- and nothing more. The category sidebar and the list views
// arrive with useLibraryList(), which is built before either view exists.
export function App() {
  const about = useQuery({
    queryKey: ["about"],
    queryFn: async () => {
      const { data, error } = await api.GET("/about");
      if (error) throw error;
      return data;
    },
  });

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center justify-between border-b border-border-subtle bg-surface px-4 py-3">
        <h1 className="text-sm font-semibold tracking-tight">MediaNexus</h1>
        <ThemeToggle />
      </header>

      <main className="flex-1 overflow-auto p-6">
        <section className="mx-auto max-w-2xl space-y-4">
          <h2 className="text-lg font-medium">The shell is running</h2>
          <p className="text-sm text-ink-muted">
            The library views are not built yet. This page exists to prove the bundle is
            served from the binary and that the generated API client reaches the server.
          </p>

          <dl className="rounded-lg border border-border-subtle bg-surface p-4 text-sm">
            {about.isPending && <p className="text-ink-muted">Asking the server who it is…</p>}
            {about.isError && (
              <p className="text-danger">
                Could not reach the API. In development, is the server running on :8080?
              </p>
            )}
            {about.data && (
              <div className="grid grid-cols-[8rem_1fr] gap-y-2">
                <dt className="text-ink-muted">Version</dt>
                <dd className="font-mono">{about.data.version}</dd>
                <dt className="text-ink-muted">Commit</dt>
                <dd className="font-mono">{about.data.commit}</dd>
                <dt className="text-ink-muted">Licence</dt>
                <dd>{about.data.license}</dd>
                <dt className="text-ink-muted">Source</dt>
                <dd>
                  <a
                    className="text-accent underline underline-offset-2"
                    href={about.data.source_url}
                  >
                    {about.data.source_url}
                  </a>
                </dd>
              </div>
            )}
          </dl>
        </section>
      </main>
    </div>
  );
}
