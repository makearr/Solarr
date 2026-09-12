import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Library as LibraryIcon } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { defaultLevelKey } from "./Sidebar";

type RootFolder = components["schemas"]["RootFolder"];
type LibraryEntry = components["schemas"]["Library"];

/** The landing page -- the *Arr apps' own default view, asked for
 *  2026-09-11: libraries first, not a cross-library stats screen. Configured
 *  storage and work in flight stay below, because "is this thing set up and
 *  is it doing anything" still matters -- just second to "what's in it".
 *  One request each: section 21.12 wants these figures precomputed, not
 *  aggregated live. */
export function Dashboard() {
  const summary = useQuery({
    queryKey: ["dashboard"],
    queryFn: async () => {
      const { data, error } = await api.GET("/dashboard");
      if (error) throw error;
      return data;
    },
  });
  const libraries = useQuery({
    queryKey: ["libraries"],
    queryFn: async () => {
      const { data, error } = await api.GET("/libraries");
      if (error) throw error;
      return data;
    },
  });

  if (summary.isLoading) return <Placeholder>Loading…</Placeholder>;
  if (summary.error !== null || !summary.data) {
    return <Placeholder>The dashboard could not be read.</Placeholder>;
  }

  const { root_folders: roots, jobs, library } = summary.data;
  const offline = roots.filter((root) => !root.online);
  const unwritable = roots.filter((root) => root.writable === false);

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <Section title="Libraries">
          {libraries.data && libraries.data.length > 0 ? (
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
              {libraries.data.map((entry) => (
                <LibraryCard key={entry.id} library={entry} />
              ))}
            </div>
          ) : (
            <Note>
              No library yet.{" "}
              <Link to="/storage/roots" className="text-accent underline underline-offset-2">
                Add a root folder
              </Link>{" "}
              and a category to create one.
            </Note>
          )}
        </Section>

        <Section title="Activity">
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
            <Tile label="Queued" value={jobs.pending} to="/activity" />
            <Tile label="Running" value={jobs.running} to="/activity" tone="accent" />
            <Tile label="Failed" value={jobs.failed} to="/activity" tone="danger" />
            <Tile label="Done" value={jobs.succeeded} to="/activity" />
            <Tile label="Cancelled" value={jobs.cancelled} to="/activity" />
          </div>
        </Section>

        {library.probed + library.unprobed > 0 && (
          <Section title="Probe coverage">
            <Link
              to="/library/health"
              className="flex items-center gap-3 rounded-lg border border-border-subtle
                         bg-surface px-3 py-2.5 text-sm hover:border-accent
                         focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              <span className="font-mono tabular-nums">
                {library.probed.toLocaleString()} of{" "}
                {(library.probed + library.unprobed).toLocaleString()} files probed
              </span>
              {library.unprobed > 0 && (
                <span className="ml-auto shrink-0 text-xs text-ink-muted">
                  {library.unprobed.toLocaleString()} still unmeasured — view library health →
                </span>
              )}
            </Link>
          </Section>
        )}

        <Section title={`Storage (${roots.length})`}>
          {roots.length === 0 ? (
            <Note>
              No library root is configured. <Link to="/settings" className="text-accent underline
                underline-offset-2">Add one in settings</Link> before importing anything.
            </Note>
          ) : (
            <ul className="divide-y divide-border-subtle rounded-lg border border-border-subtle
                           bg-surface">
              {roots.map((root) => (
                <Root key={root.id} root={root} />
              ))}
            </ul>
          )}
          {(offline.length > 0 || unwritable.length > 0) && (
            <Note tone="warn">
              {offline.length > 0 && `${offline.length} root offline. `}
              {unwritable.length > 0 &&
                `${unwritable.length} root not writable by this process — reported, never corrected.`}
            </Note>
          )}
        </Section>
      </div>
    </div>
  );
}

function LibraryCard({ library }: { library: LibraryEntry }) {
  const target = defaultLevelKey(library);
  const primaryLevel = library.levels[0];
  return (
    <Link
      to={target ? "/library/$categoryId/$level" : "/library/$categoryId/overview"}
      params={{ categoryId: String(library.id), level: target ?? "" }}
      className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3
                 hover:border-accent focus-visible:outline-2 focus-visible:outline-offset-2
                 focus-visible:outline-accent"
    >
      <span className="flex items-center gap-2">
        <LibraryIcon aria-hidden className="size-4 shrink-0 text-ink-muted" />
        <span className="truncate font-medium">{library.name}</span>
      </span>
      <span className="text-xs text-ink-muted">
        {library.media_type}
        {primaryLevel?.count != null && ` · ${primaryLevel.count.toLocaleString()} ${primaryLevel.label.toLowerCase()}`}
      </span>
    </Link>
  );
}

function Root({ root }: { root: RootFolder }) {
  return (
    <li className="flex items-center gap-3 px-3 py-2 text-sm">
      <span className="truncate font-medium">{root.label ?? root.path}</span>
      {root.label && (
        <span className="truncate font-mono text-xs text-ink-muted">{root.path}</span>
      )}
      <span className="ml-auto flex shrink-0 items-center gap-2 text-xs">
        {root.writable === false && <Pill tone="warn">read-only</Pill>}
        <Pill tone={root.online ? "ok" : "danger"}>{root.online ? "online" : "offline"}</Pill>
      </span>
    </li>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">{title}</h2>
      {children}
    </section>
  );
}

/** Every figure links into the view it summarises (section 21.12): a number
 *  nobody can drill into is a number nobody can act on. */
function Tile({
  label,
  value,
  to,
  search,
  tone,
}: {
  label: string;
  value: number;
  to: string;
  search?: Record<string, string>;
  tone?: "accent" | "danger" | "warn";
}) {
  const emphasis =
    value === 0
      ? "text-ink-muted"
      : tone === "danger"
        ? "text-danger"
        : tone === "warn"
          ? "text-warn"
          : tone === "accent"
            ? "text-accent"
            : "text-ink";
  return (
    <Link
      to={to}
      search={search}
      className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface px-3 py-2.5
                 hover:border-accent focus-visible:outline-2 focus-visible:outline-offset-2
                 focus-visible:outline-accent"
    >
      <span className="text-xs text-ink-muted">{label}</span>
      <span className={`font-mono text-xl tabular-nums ${emphasis}`}>
        {value.toLocaleString()}
      </span>
    </Link>
  );
}

function Pill({ children, tone }: { children: React.ReactNode; tone: "ok" | "warn" | "danger" }) {
  const colour =
    tone === "ok" ? "text-ok" : tone === "warn" ? "text-warn" : "text-danger";
  return (
    <span className={`rounded-full border border-current px-1.5 py-px ${colour}`}>{children}</span>
  );
}

function Note({ children, tone }: { children: React.ReactNode; tone?: "warn" }) {
  return (
    <p className={`text-xs ${tone === "warn" ? "text-warn" : "text-ink-muted"}`}>{children}</p>
  );
}

function Placeholder({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
