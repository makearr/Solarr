import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";

import * as query from "./query";
import * as selection from "./selection";
import type { ViewState } from "./useRouteView";

/** A page as every collection endpoint returns it: items plus an opaque cursor. */
export interface Page<T> {
  items: T[];
  next_cursor?: string;
}

export interface ListSource<T> {
  /** Distinguishes one list's cache from another's. */
  readonly name: string;
  readonly idOf: (item: T) => selection.Id;
  readonly fetchPage: (request: {
    cursor?: string;
    query: query.ListQuery;
    signal: AbortSignal;
  }) => Promise<Page<T>>;
  /** Event topics whose arrival should invalidate this list (ADR-9). */
  readonly topics?: readonly string[];
  readonly rowHeight?: number;
}

const DEFAULT_ROW_HEIGHT = 36;

/** The headless core both library views render (section 21.2).
 *  See documentation/web-interface.md. */
export function useLibraryList<T>(source: ListSource<T>, view: ViewState) {
  const client = useQueryClient();
  const scrollRef = useRef<HTMLDivElement | null>(null);

  const listQuery = view.query;
  const [selected, setSelected] = useState<selection.Selection>(selection.NOTHING);
  const [lastToggled, setLastToggled] = useState<selection.Id | null>(null);

  // The view state is the URL, so a navigation into a filtered view arrives here
  // as a re-render. Holding a copy in state would ignore it (section 21.12).
  const setQuery = useCallback(
    (next: query.ListQuery) => {
      view.setQuery(next);
      // Or it acts on rows the user can no longer see.
      setSelected(selection.NOTHING);
      setLastToggled(null);
    },
    [view],
  );

  const queryKey = useMemo(
    () => [source.name, query.key(listQuery)] as const,
    [source.name, listQuery],
  );

  const pages = useInfiniteQuery({
    queryKey,
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam, signal }) =>
      source.fetchPage({ cursor: pageParam, query: listQuery, signal }),
    // undefined stops TanStack asking again; an empty string would loop.
    getNextPageParam: (last: Page<T>) => last.next_cursor || undefined,
  });

  const items = useMemo(
    () => pages.data?.pages.flatMap((page) => page.items) ?? [],
    [pages.data],
  );
  const loadedIds = useMemo(() => items.map(source.idOf), [items, source]);

  const virtualizer = useVirtualizer({
    // One extra row: the sentinel that triggers the next fetch.
    count: items.length + (pages.hasNextPage ? 1 : 0),
    getScrollElement: () => scrollRef.current,
    estimateSize: () => source.rowHeight ?? DEFAULT_ROW_HEIGHT,
    overscan: 8,
  });

  // From the window, not a scroll handler, which at 500k rows fires far more
  // often than it needs to.
  const virtualItems = virtualizer.getVirtualItems();
  const lastVisible = virtualItems[virtualItems.length - 1]?.index ?? 0;
  useEffect(() => {
    if (pages.hasNextPage && !pages.isFetchingNextPage && lastVisible >= items.length - 1) {
      void pages.fetchNextPage();
    }
  }, [lastVisible, items.length, pages]);

  // A string, not the array: a caller writing its source inline hands over a new
  // array every render, and depending on that identity reopens the stream every
  // render. The list is meant to survive being called the obvious way.
  const topics = source.topics?.join(",") ?? "";
  const listName = source.name;

  // Events are lossy and for notification only (ADR-9), so they invalidate.
  useEffect(() => {
    if (topics === "") return;

    const stream = new EventSource(`/api/v1/events?topics=${topics}`);
    const invalidate = () => void client.invalidateQueries({ queryKey: [listName] });
    // The server names every frame "update" or "resync" (never per-topic) --
    // the ?topics= query string above is what already scopes the
    // subscription, so any "update" received here is already one of ours.
    stream.addEventListener("update", invalidate);
    // A resync means the client fell too far behind to replay.
    stream.addEventListener("resync", invalidate);

    return () => stream.close();
  }, [client, listName, topics]);

  const toggle = useCallback(
    (id: selection.Id, extendRange = false) => {
      setSelected((current) =>
        extendRange && lastToggled !== null
          ? selection.selectRange(current, loadedIds, lastToggled, id)
          : selection.toggle(current, id),
      );
      setLastToggled(id);
    },
    [lastToggled, loadedIds],
  );

  return {
    query: listQuery,
    setQuery,
    sortBy: useCallback(
      (column: string) => setQuery(query.cycleSort(listQuery, column)),
      [listQuery, setQuery],
    ),
    filterBy: useCallback(
      (key: string, value: string) => setQuery(query.withFilter(listQuery, key, value)),
      [listQuery, setQuery],
    ),

    items,
    isLoading: pages.isPending,
    isFetchingMore: pages.isFetchingNextPage,
    error: pages.error,

    selection: selected,
    isSelected: useCallback((id: selection.Id) => selection.isSelected(selected, id), [selected]),
    toggle,
    selectAllMatching: useCallback(() => setSelected(selection.ALL), []),
    clearSelection: useCallback(() => setSelected(selection.NOTHING), []),
    selectionIsEmpty: selection.isEmpty(selected),
    // Null until the server reports a total; loaded rows are not the count.
    selectionCount: selection.count(selected, null),
    selectionRequest: useMemo(
      () => selection.toRequest(selected, listQuery.filters),
      [selected, listQuery.filters],
    ),

    scrollRef,
    virtualizer,
    virtualItems,
    totalSize: virtualizer.getTotalSize(),
    hasMore: pages.hasNextPage,
  };
}

export type LibraryList<T> = ReturnType<typeof useLibraryList<T>>;
