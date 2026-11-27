import { History as HistoryIcon } from "lucide-react";
import { Link } from "@tanstack/react-router";

import { EmptyState } from "@/ui/EmptyState";
import { api } from "@/api/client";
import { ListTable, type Column } from "@/list/ListTable";
import { useLibraryList } from "@/list/useLibraryList";
import { useRouteView } from "@/list/useRouteView";
import { HistoryKind, historySource, historyWhen, type HistoryEntry } from "@/lib/downloadHistory";
import { t } from "@/lib/i18n";

const COLUMNS: readonly Column<HistoryEntry>[] = [
  { key: "at", header: "When", width: "11rem", render: historyWhen },
  { key: "kind", header: "Event", width: "6rem", render: (entry) => <HistoryKind kind={entry.kind} /> },
  {
    key: "album",
    header: "Title",
    render: (entry) =>
      entry.work_id != null ? (
        <Link to="/works/$id" params={{ id: String(entry.work_id) }} className="hover:underline">
          {[entry.artist, entry.album].filter(Boolean).join(" — ") || "Untitled"}
        </Link>
      ) : (
        (entry.album ?? "")
      ),
  },
  {
    key: "release",
    header: "Release",
    render: (entry) => (
      <span className="block truncate" title={[entry.release_title, entry.detail].filter(Boolean).join("\n")}>
        {entry.release_title}
        {entry.detail && (
          <span className={entry.kind === "failed" ? "text-danger" : "text-ink-muted"}> — {entry.detail}</span>
        )}
      </span>
    ),
  },
  { key: "source", header: "From → to", width: "14rem", render: historySource },
];

// Module scope, so the identity is stable across renders.
const FILTER_KEYS: readonly string[] = [];

/** Activity → History, as in Lidarr: every release grabbed, imported or failed, newest first. */
export function History() {
  const list = useLibraryList<HistoryEntry>(
    {
      name: "history",
      idOf: (entry) => entry.id,
      topics: ["acquisition"],
      fetchPage: async ({ cursor, signal }) => {
        const { data, error } = await api.GET("/history", {
          params: { query: { after: cursor === undefined ? undefined : Number(cursor), limit: 100 } },
          signal,
        });
        if (error) throw error;
        return { items: data.items, next_cursor: data.next_cursor?.toString() };
      },
    },
    useRouteView(FILTER_KEYS),
  );

  return (
    <div className="flex h-full flex-col">
      {/* The section tab already names the page on screen. */}
      <h1 className="sr-only">History</h1>
      <div className="min-h-0 flex-1">
        {list.isLoading && <Message>Loading history…</Message>}
        {list.error !== null && <Message>{t("history.readFailed")}</Message>}
        {!list.isLoading && list.error === null && list.items.length === 0 && (
          <EmptyState icon={<HistoryIcon aria-hidden className="size-6" />} title={t("history.empty")} />
        )}
        {list.items.length > 0 && <ListTable list={list} columns={COLUMNS} idOf={(entry) => entry.id} />}
      </div>
    </div>
  );
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
