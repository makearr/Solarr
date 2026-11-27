import { Link } from "@tanstack/react-router";
import { PageHeader } from "@/ui/PageHeader";
import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";

type UnprobedFile = components["schemas"]["UnprobedFile"];
type IncompleteWork = components["schemas"]["IncompleteWork"];
type RootNearReserve = components["schemas"]["RootNearReserve"];
type BlockedPresent = components["schemas"]["BlockedPresent"];
type CategoryOfflineFiles = components["schemas"]["CategoryOfflineFiles"];

/** Todo 4.8: probe coverage, unmatched files, incomplete works, roots near
 *  their own reserve, and blocked hashes still sitting in the library
 *  (ADR-53, Invariant 83 -- "reported, never touched", so nothing here
 *  removes anything). Every count is exact; every list beside one is a
 *  bounded sample (the first 20), not a second paginated browser -- "every
 *  figure a link" means a concrete instance, not that this page replaces
 *  the identification queue or the blocklist screens. */
export function LibraryHealth() {
  const health = useQuery({
    queryKey: ["library-health"],
    queryFn: () => ok(api.GET("/library/health")),
  });

  if (health.isLoading) return <Placeholder>Loading…</Placeholder>;
  if (health.error !== null || !health.data) {
    return <Placeholder>{t("libraryHealth.libraryHealthCouldNot")}</Placeholder>;
  }
  const report = health.data;

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <header className="flex flex-col gap-1">
          <PageHeader title={t("pageTitle.libraryHealth")} />
          <p className="text-xs text-ink-muted">
            A blocked hash found here was never touched -- remove it from the Unwanted files
            screen if it should go.
          </p>
        </header>

        <Section title={t("libraryHealth.unprobedFiles")} count={report.unprobed_count}>
          {report.unprobed_count === 0 ? (
            <Note>{t("libraryHealth.everyFileHasMeasured")}</Note>
          ) : (
            <FileList files={report.unprobed_files} />
          )}
        </Section>

        <Section title={t("libraryHealth.unmatchedFiles")} count={report.unmatched_count}>
          {report.unmatched_count === 0 ? (
            <Note>{t("libraryHealth.nothingIsWaitingFor")}</Note>
          ) : (
            <Link to="/unidentified" className="text-xs text-accent underline underline-offset-2">
              Review in the identification queue →
            </Link>
          )}
        </Section>

        <Section title={t("libraryHealth.incompleteWorks")} count={report.incomplete_count}>
          {report.incomplete_count === 0 ? (
            <Note>{t("libraryHealth.everyMonitoredWorkHas")}</Note>
          ) : (
            <IncompleteWorkList works={report.incomplete_works} />
          )}
        </Section>

        <Section title={t("libraryHealth.rootsNearReserve")} count={report.roots_near_reserve.length}>
          {report.roots_near_reserve.length === 0 ? (
            <Note>{t("libraryHealth.noRootIsClose")}</Note>
          ) : (
            <RootList roots={report.roots_near_reserve} />
          )}
        </Section>

        <Section title={t("libraryHealth.blockedHashesPresent")} count={report.blocked_present.length}>
          {report.blocked_present.length === 0 ? (
            <Note>{t("libraryHealth.noBlockedHashWas")}</Note>
          ) : (
            <BlockedList entries={report.blocked_present} />
          )}
        </Section>

        <Section title={t("libraryHealth.categoriesAffectedByAn")} count={report.categories_offline_files.length}>
          {report.categories_offline_files.length === 0 ? (
            <Note>{t("libraryHealth.noCategoryCurrentlyHas")}</Note>
          ) : (
            <CategoryOfflineList categories={report.categories_offline_files} />
          )}
        </Section>
      </div>
    </div>
  );
}

function FileList({ files }: { files: UnprobedFile[] }) {
  return (
    <ul className="divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface text-xs">
      {files.map((file) => (
        <li key={`${file.root_folder_id}:${file.rel_path}`} className="px-3 py-2">
          <div className="truncate font-mono">{file.rel_path}</div>
          {file.ffprobe_output !== "" && (
            <details className="mt-1">
              <summary className="cursor-pointer text-ink-muted">{t("health.ffprobeOutput")}</summary>
              <pre className="mt-1 max-h-48 overflow-auto whitespace-pre-wrap font-mono text-ink-muted">{file.ffprobe_output}</pre>
            </details>
          )}
        </li>
      ))}
    </ul>
  );
}

function IncompleteWorkList({ works }: { works: IncompleteWork[] }) {
  return (
    <ul className="divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface text-xs">
      {works.map((work) => (
        <li key={work.work_id} className="flex items-center gap-2 px-3 py-2">
          <Link
            to="/works/$id"
            params={{ id: String(work.work_id) }}
            className="truncate text-accent underline underline-offset-2"
          >
            {work.title}
          </Link>
          <span className="ml-auto shrink-0 font-mono text-ink-muted">
            {work.present_count} of {work.required_count ?? "?"}
          </span>
        </li>
      ))}
    </ul>
  );
}

function RootList({ roots }: { roots: RootNearReserve[] }) {
  return (
    <ul className="divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface text-xs">
      {roots.map((root) => (
        <li key={root.root_folder_id} className="flex items-center gap-2 px-3 py-2">
          <span className="truncate font-mono">{root.path}</span>
          <span className="ml-auto shrink-0 text-warn">
            {formatBytes(root.free_bytes)} free (reserve {formatBytes(root.min_free_bytes)})
          </span>
        </li>
      ))}
    </ul>
  );
}

function CategoryOfflineList({ categories }: { categories: CategoryOfflineFiles[] }) {
  return (
    <ul className="divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface text-xs">
      {categories.map((category) => (
        <li key={category.category_id} className="flex items-center gap-2 px-3 py-2">
          <span className="truncate font-medium">{category.name}</span>
          <span className="ml-auto shrink-0 text-warn">
            {category.offline_files.toLocaleString()} of {category.total_files.toLocaleString()} files unreachable
            -- root offline
          </span>
        </li>
      ))}
    </ul>
  );
}

function BlockedList({ entries }: { entries: BlockedPresent[] }) {
  return (
    <ul className="divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface text-xs">
      {entries.map((entry) => (
        <li key={`${entry.blocked_content_id}:${entry.media_object_id}`} className="flex flex-col gap-0.5 px-3 py-2">
          <span className="flex items-center gap-2">
            <span className="font-medium">{entry.title || "(untitled)"}</span>
            <span className="ml-auto shrink-0 text-danger">{entry.reason}</span>
          </span>
          <span className="truncate font-mono text-ink-muted">{entry.rel_path}</span>
        </li>
      ))}
    </ul>
  );
}

function formatBytes(bytes: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(1)} ${units[unit]}`;
}

function Section({
  title,
  count,
  children,
}: {
  title: string;
  count: number;
  children: React.ReactNode;
}) {
  return (
    <section className="flex flex-col gap-2">
      <h2 className="flex items-baseline gap-2 text-xs font-medium uppercase tracking-wide text-ink-muted">
        {title}
        <span className={`font-mono normal-case tracking-normal ${count > 0 ? "text-ink" : "text-ink-muted"}`}>
          {count.toLocaleString()}
        </span>
      </h2>
      {children}
    </section>
  );
}

function Note({ children }: { children: React.ReactNode }) {
  return <p className="text-xs text-ink-muted">{children}</p>;
}

function Placeholder({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
