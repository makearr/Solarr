import { tr } from "@/lib/i18nRich";
import { EmptyState } from "@/ui/EmptyState";
import { t } from "@/lib/i18n";
import { Fragment } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, Plus, SlidersHorizontal, X, XCircle , Inbox } from "lucide-react";

import { api, ok } from "@/api/client";
import { healthMessage } from "@/lib/healthMessage";
import { mediaTypeIcon, mediaTypeName } from "@/lib/mediaTypeIcon";
import { ADMINISTRATOR_WIDGETS, useAttentionDismissed, useDashboardWidgets, type DashboardWidget } from "@/lib/interfacePreferences";
import { useLibraryTree } from "@/lib/resources";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { isTierVisible, useTier } from "@/lib/useTier";
import type { components } from "@/api/schema";

import { ArtistCoverMark, Poster } from "./Poster";
import { DiscoverRow } from "./DiscoverRow";
import { DashboardDownloads } from "./DashboardDownloads";
import { SetupChecklist } from "./DashboardSetup";
import { SearchBox } from "./SearchBox";
import { pageTier } from "./sections";
import { defaultLevelKey } from "./Sidebar";

type LibraryEntry = components["schemas"]["Library"];

// Seerr's own landing shape, asked for 2026-09-13: find something or add
// something, then what has just arrived. Ten is what fits one poster row on a
// wide screen without a second query.
const RECENT_LIMIT = 5;

// A scan moves in batches; a slower poll than the status bar is enough for a figure this coarse.
const SCAN_REFRESH_MS = 10_000;

/** One scan: its running count against what the last scan found. The last count is an estimate, so the
 *  bar stops short of full and a first scan shows the count alone. */
function ScanLine({ scan }: { scan: components["schemas"]["ScanRun"] }) {
  const expected = scan.files_expected;
  const share = expected > 0 ? Math.min(scan.files_seen / expected, 0.99) : null;
  const seen = scan.files_seen.toLocaleString();
  return (
    <li className="rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm">
      <div className="flex items-baseline justify-between gap-3">
        <span className="min-w-0 truncate" title={scan.path}>{scan.library || scan.path}</span>
        <span className="shrink-0 text-xs tabular-nums text-ink-muted">
          {share !== null ? `${seen} of about ${expected.toLocaleString()} files` : `${seen} ${scan.full ? "" : "changed "}files so far`}
        </span>
      </div>
      {share !== null && (
        <div
          role="progressbar"
          aria-label={`Scan of ${scan.library || scan.path}`}
          aria-valuenow={Math.round(share * 100)}
          aria-valuemin={0}
          aria-valuemax={100}
          className="mt-1.5 h-1 overflow-hidden rounded-full bg-border-subtle"
        >
          <div className="h-full bg-accent" style={{ width: `${share * 100}%` }} />
        </div>
      )}
    </li>
  );
}

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

  // The checks name configuration and paths, which only an administrator may read.
  const administrator = useIsAdministrator();
  const health = useQuery({
    queryKey: ["system-health"],
    queryFn: () => ok(api.GET("/system/health")),
    enabled: administrator,
  });
  const [widgets] = useDashboardWidgets();

  const scans = useQuery({
    queryKey: ["scans"],
    queryFn: () => ok(api.GET("/scans")),
    refetchInterval: SCAN_REFRESH_MS,
  });

  const firstLibrary = libraries.data?.[0];
  // A warning whose fix lives on a page this tier hides is not shown (2026-09-29).
  const tier = useTier();
  const checks = health.data?.filter((check) => check.link == null || isTierVisible(tier, pageTier(check.link)));

  const [dismissedChecks, setDismissedChecks] = useAttentionDismissed();
  const checkKey = (check: { id: string; message: string }) => `${check.id}:${check.message}`;
  const hasNewCheck = (checks ?? []).some((check) => !dismissedChecks.includes(checkKey(check)));

  const nodes: Record<DashboardWidget, React.ReactNode> = {
    setup: <SetupChecklist />,
    attention: (
        checks && checks.length > 0 && hasNewCheck && (
          <Section
            title={t("dashboard.needsAttention")}
            onDismiss={() => setDismissedChecks(checks.map(checkKey))}
            dismissLabel={t("dashboard.hideNeedsAttention")}
          >
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
                  <span className="flex-1">{healthMessage(check)}</span>
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
        )
    ),
    scanning: (
        scans.data && scans.data.scans.length > 0 && (
          <Section title="Scanning">
            <ul className="flex flex-col gap-2">
              {scans.data.scans.map((scan) => (
                <ScanLine key={scan.root_folder_id} scan={scan} />
              ))}
            </ul>
          </Section>
        )
    ),
    downloads: (
        <Section title="Downloads">
          <DashboardDownloads />
        </Section>
    ),
    discover: <DiscoverRow />,
    recent: (
        <Section title={t("dashboard.recentlyAdded")}>
          {recent.isLoading && <Note>Loading…</Note>}
          {recent.error != null && <Note>{t("dashboard.recentlyAddedCouldNot")}</Note>}
          {recent.data && recent.data.items.length === 0 && (
            <EmptyState icon={<Inbox aria-hidden className="size-6" />} title={t("dashboard.nothingYetImportedReleases")} action={<Link to="/add" className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink">{t("addAnywhere.addToLibrary")}</Link>} />
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
    ),
    libraries: <LibraryStrip libraries={libraries.data ?? []} />,
  };

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
                {t("addAnywhere.addToLibrary")}
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
            <Link
              to="/settings/webui"
              className="ml-auto flex items-center gap-1.5 rounded-md px-3 py-2 text-sm text-ink-muted hover:text-ink
                         focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              <SlidersHorizontal aria-hidden className="size-4" />
              {t("dashboard.customise")}
            </Link>
          </div>
        </section>

        {widgets.filter((entry) => entry.shown && (administrator || !ADMINISTRATOR_WIDGETS.has(entry.widget))).map((entry) => (
          <Fragment key={entry.widget}>{nodes[entry.widget]}</Fragment>
        ))}

        {summary.isLoading && <Note>Loading…</Note>}
        {(summary.error !== null || !summary.data) && !summary.isLoading && (
          <Note>{t("dashboard.theStatusPanelsCould")}</Note>
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

/** One slim row per library: what it holds and what it still wants; a click opens it. */
function LibraryStrip({ libraries }: { libraries: readonly LibraryEntry[] }) {
  if (libraries.length === 0) {
    return (
      <Section title={t("dashboard.widget.libraries")}>
        <Note>
          {tr("dashboard.noLibrary", { link: <Link to="/settings/libraries" className="text-accent underline underline-offset-2">
            Add one
          </Link> })}
        </Note>
      </Section>
    );
  }
  return (
    <Section title={t("dashboard.widget.libraries")}>
      <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface text-sm">
        {libraries.map((library) => {
          const target = defaultLevelKey(library);
          const primary = library.levels[0];
          const Icon = mediaTypeIcon(library.media_type);
          const row = (
            <>
              <Icon aria-hidden className="size-4 shrink-0 text-accent" />
              <span className="min-w-0 flex-1 truncate font-medium">{library.name}</span>
              {primary?.count != null && (
                <span className="shrink-0 tabular-nums text-ink-muted">
                  {t("dashboard.libraryItems", { count: primary.count.toLocaleString(), label: primary.label.toLowerCase() })}
                </span>
              )}
              {(library.wanted ?? 0) > 0 && (
                <span className="shrink-0 tabular-nums text-warn">{t("dashboard.libraryWanted", { count: (library.wanted ?? 0).toLocaleString() })}</span>
              )}
            </>
          );
          const className = "flex items-center gap-3 px-3 py-2 hover:bg-canvas focus-visible:outline-2 focus-visible:outline-accent";
          return (
            <li key={library.id}>
              {target ? (
                <Link to="/library/$categoryId/$level" params={{ categoryId: String(library.id), level: target }} className={className}>{row}</Link>
              ) : (
                <div className={className}>{row}</div>
              )}
            </li>
          );
        })}
      </ul>
    </Section>
  );
}

function Section({
  title, children, onDismiss, dismissLabel,
}: { title: string; children: React.ReactNode; onDismiss?: () => void; dismissLabel?: string }) {
  return (
    <section className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between">
        <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">{title}</h2>
        {onDismiss && (
          <button
            type="button"
            onClick={onDismiss}
            aria-label={dismissLabel}
            title={dismissLabel}
            className="rounded-md p-0.5 text-ink-muted hover:text-ink focus-visible:outline-2 focus-visible:outline-accent"
          >
            <X aria-hidden className="size-4" />
          </button>
        )}
      </div>
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
