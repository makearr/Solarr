import { useState } from "react";

import { Briefcase } from "lucide-react";
import { EmptyState } from "@/ui/EmptyState";
import { api, ok } from "@/api/client";
import { ListGrid } from "@/list/ListGrid";
import { ListTable, type Column } from "@/list/ListTable";
import { useLibraryList } from "@/list/useLibraryList";
import { useRouteView } from "@/list/useRouteView";
import { useNarrow } from "@/list/useNarrow";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";
import { PageHeader } from "@/ui/PageHeader";

type Job = components["schemas"]["Job"];

// Five label/value rows plus padding.
const CARD_HEIGHT = 132;

const COLUMNS: readonly Column<Job>[] = [
  { key: "id", header: "ID", width: "5rem", render: (job) => job.id },
  { key: "type", header: "Type", width: "12rem", render: (job) => job.type },
  { key: "pool", header: "Pool", width: "8rem", render: (job) => job.pool },
  { key: "state", header: "State", width: "9rem", render: (job) => <State job={job} /> },
  { key: "created_at", header: "Created", render: (job) => job.created_at },
];

// Module scope, so the identity is stable across renders.
const FILTER_KEYS = ["state"];

// Import progress frames are per-session and several a second; the list shows none of it.
export const isJobListEvent = (event: { entity: string }) => !event.entity.startsWith("import_session:");

// The first consumer of the headless core, and the only real cursor-paginated
// collection until the library has rows to list.
export function Activity() {
  const [view, setView] = useState<"table" | "grid">("table");

  const narrow = useNarrow();
  const list = useLibraryList<Job>({
    name: "jobs",
    rowHeight: narrow ? CARD_HEIGHT : undefined,
    idOf: (job) => job.id,
    topics: ["job"],
    accepts: isJobListEvent,
    fetchPage: ({ cursor, query, signal }) => ok(api.GET("/jobs", {
      params: {
        query: {
          cursor,
          limit: 100,
          ...(query.filters.state ? { state: query.filters.state as Job["state"] } : {}),
        },
      },
      signal,
    })),
  }, useRouteView(FILTER_KEYS));

  return (
    <div className="flex h-full flex-col">
      <div className="shrink-0 border-b border-border-subtle px-4 py-2">
        <PageHeader
          title="Jobs"
          facts={
        <select
          aria-label={t("activity.filterState")}
          className="rounded-md border border-border-subtle bg-canvas px-2 py-1 text-xs"
          value={list.query.filters.state ?? ""}
          onChange={(event) => list.filterBy("state", event.target.value)}
        >
          <option value="">Any state</option>
          {["PENDING", "RUNNING", "SUCCEEDED", "FAILED", "CANCELLED"].map((state) => (
            <option key={state} value={state}>
              {state}
            </option>
          ))}
        </select>
          }
          actions={
        <div className="flex items-center gap-2 text-xs">
          {!list.selectionIsEmpty && (
            <>
              <span className="text-ink-muted">
                {list.selectionCount === null ? "All matching" : `${list.selectionCount} selected`}
              </span>
              <button type="button" onClick={list.clearSelection} className={quietButton}>
                Clear
              </button>
            </>
          )}
          <button type="button" onClick={list.selectAllMatching} className={quietButton}>
            {t("activity.selectAll")}
          </button>
          <button
            type="button"
            onClick={() => setView(view === "table" ? "grid" : "table")}
            className={quietButton}
          >
            {view === "table" ? "Grid" : "Table"}
          </button>
        </div>
          }
        />
      </div>

      <div className="min-h-0 flex-1">
        {list.isLoading && <Message>Loading jobs…</Message>}
        {list.error !== null && <Message>{t("activity.readFailed")}</Message>}
        {!list.isLoading && list.error === null && list.items.length === 0 && (
          <EmptyState icon={<Briefcase aria-hidden className="size-6" />} title={t("activity.empty")} />
        )}

        {list.items.length > 0 &&
          (view === "table" ? (
            <ListTable list={list} columns={COLUMNS} idOf={(job) => job.id} />
          ) : (
            <ListGrid
              list={list}
              idOf={(job) => job.id}
              renderCard={(job) => (
                <span className="flex w-full items-center gap-3">
                  <span className="font-mono text-xs text-ink-muted">{job.id}</span>
                  <span className="truncate">{job.type}</span>
                  <span className="ml-auto">
                    <State job={job} />
                  </span>
                </span>
              )}
            />
          ))}
      </div>
    </div>
  );
}

function State({ job }: { job: Job }) {
  const tone =
    job.state === "FAILED"
      ? "text-danger"
      : job.state === "SUCCEEDED"
        ? "text-ok"
        : job.state === "RUNNING"
          ? "text-accent"
          : "text-ink-muted";
  return <span className={tone}>{job.state}</span>;
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}

const quietButton =
  "rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
