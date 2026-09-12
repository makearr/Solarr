import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";
import { ThemeToggle } from "./ThemeToggle";

/** AGPL section 13's own screen (documentation/licence-compliance.md): build
 *  identity, the Corresponding Source offer, the full licence text and
 *  third-party attributions. Reached outside the router (App.tsx checks the
 *  path before any auth gate), because "every user interacting with it
 *  remotely" includes someone who cannot -- or has not -- logged in. */
export function About() {
  const about = useQuery({
    queryKey: ["about"],
    queryFn: async () => {
      const { data, error } = await api.GET("/about");
      if (error) throw error;
      return data;
    },
    staleTime: Infinity,
  });

  const license = useQuery({
    queryKey: ["about", "license"],
    queryFn: async () => {
      const { data, error } = await api.GET("/about/license");
      if (error) throw error;
      return data;
    },
    staleTime: Infinity,
  });

  const attribution = useQuery({
    queryKey: ["about", "attribution"],
    queryFn: async () => {
      const { data, error } = await api.GET("/about/attribution");
      if (error) throw error;
      return data;
    },
    staleTime: Infinity,
  });

  return (
    <div className="flex h-full flex-col">
      <header className="flex shrink-0 items-center justify-between border-b border-border-subtle
                         bg-surface px-4 py-3">
        <a href="/" className="text-sm font-semibold tracking-tight hover:underline">
          MediaNexus
        </a>
        <ThemeToggle />
      </header>

      <main className="min-h-0 flex-1 overflow-y-auto p-6">
        <div className="mx-auto flex max-w-2xl flex-col gap-6">
          <section className="rounded-lg border border-border-subtle bg-surface p-5">
            <h1 className="text-sm font-medium">About this instance</h1>

            {about.isLoading && <p className="mt-2 text-xs text-ink-muted">Loading…</p>}
            {about.error !== null && (
              <p className="mt-2 text-xs text-danger">Could not reach the server.</p>
            )}

            {about.data && (
              <dl className="mt-3 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1.5 text-xs">
                <dt className="text-ink-muted">Version</dt>
                <dd className="font-mono">
                  {about.data.version}
                  {about.data.milestone && (
                    <span className="ml-2 text-ink-muted">"{about.data.milestone}"</span>
                  )}
                </dd>

                <dt className="text-ink-muted">Commit</dt>
                <dd className="font-mono">{about.data.commit}</dd>

                <dt className="text-ink-muted">Built</dt>
                <dd className="font-mono">{about.data.build_date}</dd>

                <dt className="text-ink-muted">SQLite driver</dt>
                <dd className="font-mono">{about.data.sqlite_driver}</dd>

                <dt className="text-ink-muted">Server time</dt>
                <dd className="font-mono">
                  {about.data.server_time}
                  {about.data.timezone && (
                    <span className="ml-1.5 text-ink-muted">{about.data.timezone}</span>
                  )}
                </dd>

                <dt className="text-ink-muted">Licence</dt>
                <dd className="font-mono">{about.data.license}</dd>

                <dt className="text-ink-muted">Corresponding source</dt>
                <dd>
                  <a
                    href={about.data.source_url}
                    target="_blank"
                    rel="noreferrer noopener"
                    className="font-mono text-accent hover:underline"
                  >
                    {about.data.source_url}
                  </a>
                </dd>
              </dl>
            )}

            <p className="mt-4 text-xs text-ink-muted">
              MediaNexus is free software: you can redistribute it and/or modify it under the
              terms of the GNU Affero General Public License as published by the Free Software
              Foundation. The link above points at the exact source this build was made from.
            </p>
          </section>

          <LegalSection
            title="Licence — GNU Affero General Public License v3"
            text={license.data?.text}
            loading={license.isLoading}
            error={license.error !== null}
          />

          <LegalSection
            title="Third-party attributions"
            text={attribution.data?.text}
            loading={attribution.isLoading}
            error={attribution.error !== null}
          />
        </div>
      </main>
    </div>
  );
}

function LegalSection({
  title,
  text,
  loading,
  error,
}: {
  title: string;
  text: string | undefined;
  loading: boolean;
  error: boolean;
}) {
  return (
    <details className="rounded-lg border border-border-subtle bg-surface p-5">
      <summary className="cursor-pointer text-sm font-medium">{title}</summary>
      {loading && <p className="mt-2 text-xs text-ink-muted">Loading…</p>}
      {error && <p className="mt-2 text-xs text-danger">Could not load this text.</p>}
      {text && (
        <pre className="mt-3 max-h-[32rem] overflow-auto whitespace-pre-wrap break-words
                        rounded-md bg-canvas p-3 font-mono text-[0.6875rem] leading-relaxed">
          {text}
        </pre>
      )}
    </details>
  );
}
