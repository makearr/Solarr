import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type RootFolder = components["schemas"]["RootFolder"];

/** The landing page. Configured storage and work in flight, because "is this
 *  thing set up and is it doing anything" is what someone opens it to learn.
 *  One request: section 21.12 wants these figures precomputed, not aggregated
 *  live, and three round trips on the first screen reads as slow. */
export function Dashboard() {
  const summary = useQuery({
    queryKey: ["dashboard"],
    queryFn: async () => {
      const { data, error } = await api.GET("/dashboard");
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
        <Section title="Library">
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
            <Tile label="Works" value={library.works} to="/library" />
            <Tile label="Files" value={library.files} to="/library" />
            <Tile label="Monitored" value={library.monitored} to="/library" />
            <Tile label="Missing" value={library.missing} to="/library" tone="warn" />
            <Tile
              label="Fresh"
              value={library.fresh}
              to="/library"
              search={{ fresh: "1" }}
              tone="accent"
            />
          </div>
          {library.works === 0 && (
            <Note>
              Nothing has been imported yet. Counts come from the counter table, so they stay
              instant whatever the library size.
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
            <div className="flex items-center gap-3 rounded-lg border border-border-subtle
                           bg-surface px-3 py-2.5 text-sm">
              <span className="font-mono tabular-nums">
                {library.probed.toLocaleString()} of{" "}
                {(library.probed + library.unprobed).toLocaleString()} files probed
              </span>
              {library.unprobed > 0 && (
                <span className="ml-auto shrink-0 text-xs text-ink-muted">
                  {library.unprobed.toLocaleString()} still unmeasured
                </span>
              )}
            </div>
            {/* No dedicated "unprobed files" list exists yet (section 21.14) --
                this stays a plain figure rather than a link to nowhere useful. */}
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
