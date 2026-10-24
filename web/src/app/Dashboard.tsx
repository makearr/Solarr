import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, Plus, XCircle } from "lucide-react";

import { api, ok } from "@/api/client";
import { mediaTypeIcon, mediaTypeName } from "@/lib/mediaTypeIcon";
import { useLibraryTree } from "@/lib/resources";
import { isTierVisible, useTier } from "@/lib/useTier";
import type { components } from "@/api/schema";

import { ArtistCoverMark, Poster } from "./Poster";
import { DashboardDownloads } from "./DashboardDownloads";
import { SearchBox } from "./SearchBox";
import { pageTier } from "./sections";
import { defaultLevelKey } from "./Sidebar";

type LibraryEntry = components["schemas"]["Library"];

// Seerr's own landing shape, asked for 2026-09-13: find something or add
// something, then what has just arrived. Ten is what fits one poster row on a
// wide screen without a second query.
const RECENT_LIMIT = 10;

/** The landing page. Search and Add lead, because those are the two things
 *  anyone opens this for; recently added follows with real cover art; the
 *  "is it set up and is it doing anything" panels stay below. */
export function Dashboard() {
  const summary = useQuery({
    queryKey: ["dashboard"],
    queryFn: () => ok(api.GET("/dashboard")),
  });
  const libraries = useLibraryTree();
  const recent = useQuery({
    queryKey: ["works", "recent"],
    queryFn: () => ok(api.GET("/works", {
      params: { query: { sort: "imported", dir: "desc", limit: RECENT_LIMIT } },
    })),
  });

  const health = useQuery({
    queryKey: ["system-health"],
    queryFn: () => ok(api.GET("/system/health")),
  });

  const firstLibrary = libraries.data?.[0];
  // A warning whose fix lives on a page this tier hides is not shown (2026-09-29).
  const tier = useTier();
  const checks = health.data?.filter((check) => check.link == null || isTierVisible(tier, pageTier(check.link)));

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-8 p-6">
        <h1 className="sr-only">Dashboard</h1>
        <section className="flex flex-col gap-3">
          <SearchBox variant="hero" />
          <div className="flex flex-wrap gap-2">
            {firstLibrary && (
              <Link
                to="/add"
                className="flex items-center gap-1.5 rounded-md bg-accent px-4 py-2 text-sm text-accent-ink
                           focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
              >
                <Plus aria-hidden className="size-4" />
                Add to library
              </Link>
            )}
            <Link
              to="/libraries"
              className="rounded-md border border-border-subtle px-4 py-2 text-sm text-ink-muted
                         hover:border-accent hover:text-ink focus-visible:outline-2
                         focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              Browse libraries
            </Link>
          </div>
        </section>

        {checks && checks.length > 0 && (
          <Section title="Needs attention">
            <ul className="flex flex-col gap-1">
              {checks.map((check) => (
                <li
                  key={`${check.id}:${check.message}`}
                  className="flex items-start gap-2 rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm"
                >
                  {check.severity === "error" ? (
                    <XCircle aria-hidden className="mt-0.5 size-4 shrink-0 text-danger" />
                  ) : (
                    <AlertTriangle aria-hidden className="mt-0.5 size-4 shrink-0 text-ink-muted" />
                  )}
                  <span className="flex-1">{check.message}</span>
                  {check.link != null && (
                    <Link
                      to={check.link}
                      className="shrink-0 text-xs text-ink-muted underline hover:text-ink
                                 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
                    >
                      Fix
                    </Link>
                  )}
                </li>
              ))}
            </ul>
          </Section>
        )}

        <Section title="Downloads">
          <DashboardDownloads />
        </Section>

        <Section title="Recently added">
          {recent.isLoading && <Note>Loading…</Note>}
          {recent.error != null && <Note>Recently added could not be read.</Note>}
          {recent.data && recent.data.items.length === 0 && (
            <Note>Nothing yet. Imported releases show up here as they land.</Note>
          )}
          {recent.data && recent.data.items.length > 0 && (
            <ul className="grid grid-cols-3 gap-4 sm:grid-cols-5">
              {recent.data.items.map((work) => (
                <li key={work.id}>
                  <Poster
                    subject="works"
                    imageId={work.no_artwork && !work.poster_url ? null : work.id}
                    title={work.title}
                    subtitle={[work.artist ?? mediaTypeName(work.media_type), work.year].filter(Boolean).join(" · ")}
                    link={{ to: "/works/$id", params: { id: String(work.id) } }}
                    badge={work.artist_cover ? <ArtistCoverMark /> : undefined}
                  />
                </li>
              ))}
            </ul>
          )}
        </Section>

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
              <Link to="/libraries" className="text-accent underline underline-offset-2">
                Add one
              </Link>{" "}
              to get started.
            </Note>
          )}
        </Section>

        {summary.isLoading && <Note>Loading…</Note>}
        {(summary.error !== null || !summary.data) && !summary.isLoading && (
          <Note>The status panels could not be read.</Note>
        )}
        {summary.data && <StatusPanels summary={summary.data} />}
      </div>
    </div>
  );
}

function StatusPanels({ summary }: { summary: components["schemas"]["DashboardSummary"] }) {
  const { jobs } = summary;

  return (
    <Section title="Activity">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
        <Tile label="Queued" value={jobs.pending} to="/activity/jobs" />
        <Tile label="Running" value={jobs.running} to="/activity/jobs" tone="accent" />
        <Tile label="Failed" value={jobs.failed} to="/activity/jobs" tone="danger" />
        <Tile label="Done" value={jobs.succeeded} to="/activity/jobs" />
        <Tile label="Cancelled" value={jobs.cancelled} to="/activity/jobs" />
      </div>
    </Section>
  );
}

function LibraryCard({ library }: { library: LibraryEntry }) {
  const target = defaultLevelKey(library);
  const primaryLevel = library.levels[0];
  const Icon = mediaTypeIcon(library.media_type);

  const body = (
    <>
      <span className="flex items-center gap-2">
        <Icon aria-hidden className="size-4 shrink-0 text-accent" />
        <span className="truncate font-medium">{library.name}</span>
      </span>
      <span className="text-xs text-ink-muted">
        {mediaTypeName(library.media_type)}
        {primaryLevel?.count != null && ` · ${primaryLevel.count.toLocaleString()} ${primaryLevel.label.toLowerCase()}`}
      </span>
    </>
  );
  const className =
    "flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3 hover:border-accent " +
    "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";

  if (!target) return <div className={className}>{body}</div>;
  return (
    <Link
      to="/library/$categoryId/$level"
      params={{ categoryId: String(library.id), level: target }}
      className={className}
    >
      {body}
    </Link>
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
      <span className={`font-mono text-xl tabular-nums ${emphasis}`}>{value.toLocaleString()}</span>
    </Link>
  );
}

function Note({ children, tone }: { children: React.ReactNode; tone?: "warn" }) {
  return <p className={`text-xs ${tone === "warn" ? "text-warn" : "text-ink-muted"}`}>{children}</p>;
}
