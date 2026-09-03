import { api } from "@/api/client";
import { ListTable, type Column } from "@/list/ListTable";
import { useLibraryList } from "@/list/useLibraryList";
import { useRouteView } from "@/list/useRouteView";
import type { components } from "@/api/schema";

type UnidentifiedFile = components["schemas"]["UnidentifiedFile"];

// F29: everything a scan recorded that the ADR-24 chain could not confidently
// resolve, so a human can point it at the right work. Read-only for now --
// stage 3 has no manual-identify write path yet, only the queue itself.
const COLUMNS: readonly Column<UnidentifiedFile>[] = [
  { key: "rel_path", header: "Path", render: (file) => file.rel_path },
  {
    key: "candidates",
    header: "Best guess",
    render: (file) => (file.candidates.length > 0 ? describeBest(file) : "No match found"),
  },
  { key: "first_seen_at", header: "First seen", width: "12rem", render: (file) => file.first_seen_at },
];

const FILTER_KEYS: readonly string[] = ["category_id"];

export function Unidentified() {
  const list = useLibraryList<UnidentifiedFile>(
    {
      name: "unidentified",
      idOf: (file) => file.media_object_id,
      fetchPage: async ({ cursor, query, signal }) => {
        const categoryId = query.filters.category_id;
        const { data, error } = await api.GET("/library/unidentified", {
          params: {
            query: {
              cursor,
              limit: 100,
              category_id: categoryId ? Number(categoryId) : undefined,
            },
          },
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
        <h2 className="text-sm font-medium">Unidentified</h2>
        <span className="text-xs text-ink-muted">
          Files a scan found but could not confidently match to a work
        </span>
      </div>

      <div className="min-h-0 flex-1">
        {list.isLoading && <Message>Loading the queue…</Message>}
        {list.error !== null && <Message>The queue could not be read.</Message>}
        {!list.isLoading && list.error === null && list.items.length === 0 && (
          <Message>Nothing here -- every scanned file identified confidently.</Message>
        )}

        {list.items.length > 0 && (
          <ListTable list={list} columns={COLUMNS} idOf={(file) => file.media_object_id} />
        )}
      </div>
    </div>
  );
}

// The ranked candidates are best-first (model.RunChain, ADR-24); the manual
// queue only needs the strongest guess at a glance, not the full list --
// that depth belongs to the full item page (§21.3), not this row.
function describeBest(file: UnidentifiedFile): string {
  const best = file.candidates[0];
  if (best === undefined) return "No match found";
  const confidence = Math.round(best.confidence * 100);
  return `${best.provider ?? best.strategy} (${confidence}%)`;
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
