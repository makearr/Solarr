import { useParams } from "@tanstack/react-router";

import { api } from "@/api/client";
import { useLibraryList } from "@/list/useLibraryList";
import { useRouteView } from "@/list/useRouteView";
import type { components } from "@/api/schema";

type LevelItem = components["schemas"]["LibraryLevelItem"];

const ROW_HEIGHT = 44;
const FILTER_KEYS: readonly string[] = [];

/** The generic browse view for any navigation level besides Albums (ADR-54)
 *  -- Tracks today, a future Artists once a reader exists. One generic row
 *  shape across every entity: only one media type exists yet to generalize a
 *  typed-per-level response from (GET /libraries/{id}/{level}'s own spec). */
export function LibraryLevelList() {
  const { categoryId, level } = useParams({ strict: false }) as {
    categoryId?: string;
    level?: string;
  };

  const list = useLibraryList<LevelItem>(
    {
      name: `library-level:${categoryId ?? ""}:${level ?? ""}`,
      rowHeight: ROW_HEIGHT,
      idOf: (item) => item.id,
      fetchPage: async ({ cursor, signal }) => {
        const { data, error } = await api.GET("/libraries/{id}/{level}", {
          params: {
            path: { id: Number(categoryId), level: level ?? "" },
            query: { cursor, limit: 100 },
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
      {list.isLoading && <Message>Loading…</Message>}
      {list.error !== null && (
        <Message>{(list.error as { detail?: string }).detail ?? "This view could not be read."}</Message>
      )}
      {!list.isLoading && list.error === null && list.items.length === 0 && (
        <Message>Nothing here yet.</Message>
      )}

      {list.items.length > 0 && (
        <div ref={list.scrollRef} className="min-h-0 flex-1 overflow-auto">
          <div style={{ height: list.totalSize, position: "relative" }}>
            {list.virtualItems.map((virtual) => {
              const item = list.items[virtual.index];
              if (!item) return null;
              return (
                <div
                  key={item.id}
                  className="absolute inset-x-0 flex items-center gap-3 border-b
                             border-border-subtle px-4 text-sm"
                  style={{ height: virtual.size, transform: `translateY(${virtual.start}px)` }}
                >
                  <span className="min-w-0 flex-1 truncate font-medium">{item.title}</span>
                  {item.subtitle && (
                    <span className="min-w-0 flex-1 truncate text-ink-muted">{item.subtitle}</span>
                  )}
                  {item.year != null && <span className="shrink-0 text-ink-muted">{item.year}</span>}
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
