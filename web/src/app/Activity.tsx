import { useState } from "react";

import { api } from "@/api/client";
import { ListGrid } from "@/list/ListGrid";
import { ListTable, type Column } from "@/list/ListTable";
import { useLibraryList } from "@/list/useLibraryList";
import { useNarrow } from "@/list/useNarrow";
import type { components } from "@/api/schema";

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

// The first consumer of the headless core, and the only real cursor-paginated
// collection until the library has rows to list.
export function Activity() {
  const [view, setView] = useState<"table" | "grid">("table");

  const narrow = useNarrow();
  const list = useLibraryList<Job>({
    name: "jobs",
    rowHeight: narrow ? CARD_HEIGHT : undefined,
    filterKeys: ["state"],
    idOf: (job) => job.id,
    topics: ["job"],
    fetchPage: async ({ cursor, query, signal }) => {
      const { data, error } = await api.GET("/jobs", {
        params: {
          query: {
            cursor,
            limit: 100,
            ...(query.filters.state ? { state: query.filters.state as Job["state"] } : {}),
          },
        },
        signal,
      });
      if (error) throw error;
      return data;
    },
  });

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-center gap-3 border-b border-border-subtle px-4 py-2">
        <h2 className="text-sm font-medium">Jobs</h2>

        <select
          aria-label="Filter by state"
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

        <div className="ml-auto flex items-center gap-2 text-xs">
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
            Select all matching
          </button>
          <button
            type="button"
            onClick={() => setView(view === "table" ? "grid" : "table")}
            className={quietButton}
          >
            {view === "table" ? "Grid" : "Table"}
          </button>
        </div>
      </div>

      <div className="min-h-0 flex-1">
        {list.isLoading && <Message>Loading jobs…</Message>}
        {list.error !== null && <Message>The job queue could not be read.</Message>}
        {!list.isLoading && list.error === null && list.items.length === 0 && (
          <Message>Nothing queued. Jobs appear here as the system works.</Message>
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
