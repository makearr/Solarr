import { useState } from "react";

import { api } from "@/api/client";
import { ListGrid } from "@/list/ListGrid";
import { ListTable, type Column } from "@/list/ListTable";
import { useLibraryList } from "@/list/useLibraryList";
import { useRouteView } from "@/list/useRouteView";
import { useNarrow } from "@/list/useNarrow";
import type { components } from "@/api/schema";

type Work = components["schemas"]["Work"];

// Title plus a year/media-type line, padding included.
const CARD_HEIGHT = 68;

const COLUMNS: readonly Column<Work>[] = [
  { key: "title", header: "Title", render: (work) => work.title },
  { key: "media_type", header: "Type", width: "8rem", render: (work) => work.media_type },
  { key: "year", header: "Year", width: "6rem", render: (work) => work.year ?? "" },
  { key: "added_at", header: "Added", width: "12rem", render: (work) => work.added_at },
];

// No filter keys yet: category scoping happens server-side (ADR-27) and there is
// no sidebar to drive a further one from (private/notes.md, "kept separate").
const FILTER_KEYS: readonly string[] = [];

export function Library() {
  const [view, setView] = useState<"table" | "grid">("table");

  const narrow = useNarrow();
  const list = useLibraryList<Work>(
    {
      name: "works",
      rowHeight: narrow ? CARD_HEIGHT : undefined,
      idOf: (work) => work.id,
      fetchPage: async ({ cursor, signal }) => {
        const { data, error } = await api.GET("/works", {
          params: { query: { cursor, limit: 100 } },
          signal,
        });
        if (error) throw error;
        return data;
      },
    },
    useRouteView(FILTER_KEYS),
  );

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-center gap-3 border-b border-border-subtle px-4 py-2">
        <h2 className="text-sm font-medium">Library</h2>

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
        {list.isLoading && <Message>Loading the library…</Message>}
        {list.error !== null && <Message>The library could not be read.</Message>}
        {!list.isLoading && list.error === null && list.items.length === 0 && (
          <Message>
            Nothing in the library yet. Identified files appear here once a scan finds them.
          </Message>
        )}

        {list.items.length > 0 &&
          (view === "table" ? (
            <ListTable list={list} columns={COLUMNS} idOf={(work) => work.id} />
          ) : (
            <ListGrid
              list={list}
              idOf={(work) => work.id}
              renderCard={(work) => (
                <span className="flex w-full flex-col gap-0.5">
                  <span className="truncate font-medium">{work.title}</span>
                  <span className="text-ink-muted">
                    {work.media_type}
                    {work.year ? ` · ${work.year}` : ""}
                  </span>
                </span>
              )}
            />
          ))}
      </div>
    </div>
  );
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}

const quietButton =
  "rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
