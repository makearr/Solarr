import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";

import * as query from "./query";
import * as selection from "./selection";

/** A page as every collection endpoint returns it: items plus an opaque cursor. */
export interface Page<T> {
  items: T[];
  next_cursor?: string;
}

export interface ListSource<T> {
  /** Distinguishes one list's cache from another's. */
  readonly name: string;
  /** Which query parameters this list treats as filters, for the URL. */
  readonly filterKeys: readonly string[];
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

/**
 * The headless core every library view is built on (section 21.2).
 *
 * It owns fetching, cursor pagination, sort and filter state, the selection
 * model and virtualisation. `ListTable` and `ListGrid` render what it returns
 * and decide nothing. A new filter, column or bulk action is implemented once,
 * here -- which is the difference between the table/grid toggle costing a
 * little and costing double.
 */
export function useLibraryList<T>(source: ListSource<T>) {
  const client = useQueryClient();
  const scrollRef = useRef<HTMLDivElement | null>(null);

  const [listQuery, setListQuery] = useState<query.ListQuery>(() =>
    query.fromSearch(window.location.search, source.filterKeys),
  );
  const [selected, setSelected] = useState<selection.Selection>(selection.NOTHING);
  const [lastToggled, setLastToggled] = useState<selection.Id | null>(null);

  // The URL reflects the view so it can be linked and bookmarked (section
  // 21.2). replaceState, not pushState: changing a filter is not a navigation,
  // and making it one turns Back into "undo one filter" thirty times over.
  useEffect(() => {
    const next = `${window.location.pathname}${query.toSearch(listQuery)}`;
    if (next !== `${window.location.pathname}${window.location.search}`) {
      window.history.replaceState(null, "", next);
    }
  }, [listQuery]);

  const queryKey = useMemo(
    () => [source.name, query.key(listQuery)] as const,
    [source.name, listQuery],
  );

  const pages = useInfiniteQuery({
    queryKey,
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam, signal }) =>
      source.fetchPage({ cursor: pageParam, query: listQuery, signal }),
    // An absent cursor is the last page. Returning undefined is what stops
    // TanStack asking again -- an empty string would loop.
    getNextPageParam: (last: Page<T>) => last.next_cursor || undefined,
  });

  const items = useMemo(
    () => pages.data?.pages.flatMap((page) => page.items) ?? [],
    [pages.data],
  );
  const loadedIds = useMemo(() => items.map(source.idOf), [items, source]);

  const virtualizer = useVirtualizer({
    // One extra row when more pages exist: it is the sentinel that triggers the
    // next fetch, and it also stops the scrollbar claiming the list has ended.
    count: items.length + (pages.hasNextPage ? 1 : 0),
    getScrollElement: () => scrollRef.current,
    estimateSize: () => source.rowHeight ?? DEFAULT_ROW_HEIGHT,
    overscan: 8,
  });

  // Fetch the next page when its sentinel comes into the window, rather than on
  // a scroll handler: the virtualiser already knows what is visible, and a
  // scroll listener at 500k rows fires far more often than it needs to.
  const virtualItems = virtualizer.getVirtualItems();
  const lastVisible = virtualItems[virtualItems.length - 1]?.index ?? 0;
  useEffect(() => {
    if (pages.hasNextPage && !pages.isFetchingNextPage && lastVisible >= items.length - 1) {
      void pages.fetchNextPage();
    }
  }, [lastVisible, items.length, pages]);

  // Events are lossy and for notification only (ADR-9), so they invalidate
  // rather than carrying data. Polling instead would multiply requests across a
  // 500k library for data the stream already says is unchanged.
  useEffect(() => {
    if (!source.topics || source.topics.length === 0) return;

    const stream = new EventSource(`/api/v1/events?topics=${source.topics.join(",")}`);
    const invalidate = () => void client.invalidateQueries({ queryKey: [source.name] });
    for (const topic of source.topics) stream.addEventListener(topic, invalidate);
    // A client too far behind is told to resync rather than given a partial
    // history, and the only correct response is to refetch.
    stream.addEventListener("resync", invalidate);

    return () => stream.close();
  }, [client, source.name, source.topics]);

  const setQuery = useCallback((next: query.ListQuery) => {
    setListQuery(next);
    // The selection is scoped to the filters it was made under. Keeping it
    // across a filter change would silently act on rows the user can no longer
    // see, which for "all matching" means acting on a different set entirely.
    setSelected(selection.NOTHING);
    setLastToggled(null);
  }, []);

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
    // Null until the server reports a total. Section 21.7's preview needs a
    // truthful count, and the number of loaded rows is not it.
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
