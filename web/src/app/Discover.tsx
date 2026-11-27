import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ExternalTitle } from "./ExternalItem";
import { EyeOff, Plus, Undo2 } from "lucide-react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { useEffect, useState } from "react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t, tOr, type MessageKey } from "@/lib/i18n";
import { remoteImage } from "@/lib/remoteImage";
import { Button } from "@/ui/Button";
import { Chip } from "@/ui/Chip";
import { EmptyState } from "@/ui/EmptyState";
import { useDiscoverLibraries } from "@/lib/resources";
import { Toolbar, ToolbarSearch, ToolbarSelect } from "@/ui/Toolbar";

import { useRecommendationDismissal } from "./recommendationActions";

import { AddToLibrary } from "./work/AddToSolarr";

type Recommendation = components["schemas"]["Recommendation"];
type DiscoverLibrary = components["schemas"]["DiscoverLibrary"];

const LISTS: Record<string, MessageKey> = {
  highest_rated: "discover.list.highest_rated",
  trending: "discover.list.trending",
  upcoming: "discover.list.upcoming",
  classics: "discover.list.classics",
  collections: "discover.list.collections",
};

/** The router parses a search value as JSON, so `?library=2` arrives as a number and `?library=%222%22` as text; both mean the same. */
export type DiscoverSearch = { library?: string | number; list?: string; kind?: string; genre?: string; q?: string; hidden?: string | number | boolean };

/** The library a link names, as text, or undefined. */
export function searchLibrary(search: DiscoverSearch): string | undefined {
  return search.library === undefined ? undefined : String(search.library);
}

/** Whether a link asks for the Hidden view, whether it wrote hidden=1, hidden="1" or hidden=true. */
export function searchHidden(search: DiscoverSearch): boolean {
  return search.hidden === true || String(search.hidden) === "1";
}

/** A card is 10rem wide at least; the grid gap is 1rem. */
const CARD_MIN = 160;
const GAP = 16;
/** Title, one line of facts and the list chips under a 2:3 poster. */
const CARD_TEXT = 150;
/** The next page is asked for when the last row on screen is this close to the end. */
const PREFETCH_ROWS = 3;

/** What the libraries do not hold yet, from their sources' charts, one library at a time. */
export function Discover() {
  const search = useSearch({ strict: false }) as DiscoverSearch;
  const navigate = useNavigate();
  const libraries = useDiscoverLibraries();
  const change = (patch: DiscoverSearch) =>
    void navigate({ to: "/discover", search: ((previous: DiscoverSearch) => ({ ...previous, ...patch })) as never });
  const all = libraries.data ?? [];
  // No library in the address is the one feed of all of them.
  const current = all.find((library) => String(library.category_id) === searchLibrary(search));
  const hidden = searchHidden(search);
  if (libraries.isLoading) return <p className="p-6 text-sm text-ink-muted">{t("discover.loading")}</p>;
  if (all.length === 0) return <EmptyState title={t("discover.nothing")} hint={t("discover.nothingHint")} />;
  const chip = (selected: boolean) => `rounded-md px-2 py-1 ${selected ? "bg-accent/10 text-ink" : "text-ink-muted hover:text-ink"}`;
  return (
    <div className="flex h-full flex-col">
      <Toolbar
        title={t("discover.page")}
        chips={(
          <div role="tablist" className="flex flex-wrap gap-1">
            <button role="tab" aria-selected={!current && !hidden} onClick={() => change({ library: undefined, hidden: undefined, genre: undefined, kind: undefined })} className={chip(!current && !hidden)}>
              {t("discover.allLists")}
            </button>
            {all.map((library) => (
              <button
                key={library.category_id}
                role="tab"
                aria-selected={library.category_id === current?.category_id && !hidden}
                onClick={() => change({ library: library.category_id, hidden: undefined, genre: undefined, kind: undefined })}
                className={chip(library.category_id === current?.category_id && !hidden)}
              >
                {tabName(library, all)}
              </button>
            ))}
            <button role="tab" aria-selected={hidden} onClick={() => change({ hidden: 1 })} className={chip(hidden)}>
              {t("discover.hiddenView")}
            </button>
          </div>
        )}
      >
        {!hidden && <Filters libraries={all} library={current} search={search} change={change} />}
      </Toolbar>
      {hidden ? <HiddenList /> : <Wall libraries={all} library={current} search={search} />}
    </div>
  );
}

/** Two libraries of different types may share a name; the type tells them apart. */
function tabName(library: DiscoverLibrary, all: DiscoverLibrary[]) {
  const same = all.filter((other) => other.name === library.name).length > 1;
  return same ? `${library.name} (${library.media_type})` : library.name;
}

/** What this person said they are not interested in, each with a way back. */
function HiddenList() {
  const hidden = useQuery({ queryKey: ["discover-dismissals"], queryFn: () => ok(api.GET("/discover/dismissals")) });
  const { restore, restoring, restoringItem } = useRecommendationDismissal();
  if (hidden.data?.length === 0) return <EmptyState title={t("discover.noHidden")} hint={t("discover.noHiddenHint")} />;
  return (
    <ul className="flex-1 overflow-y-auto p-4 text-sm">
      {(hidden.data ?? []).map((item) => (
        <li key={`${item.kind}:${item.provider}:${item.external_id}`} className="flex items-center gap-3 border-b border-border-subtle py-2">
          <span className="min-w-0 flex-1 truncate">{item.title}</span>
          <span className="text-xs text-ink-muted">{item.media_type}</span>
          <Button variant="ghost" icon={<Undo2 aria-hidden className="size-3.5" />} busy={restoring && restoringItem === item} onClick={() => restore(item)}>
            {t("discover.restore")}
          </Button>
        </li>
      ))}
    </ul>
  );
}

function Filters({ libraries, library, search, change }: { libraries: DiscoverLibrary[]; library: DiscoverLibrary | undefined; search: DiscoverSearch; change: (patch: DiscoverSearch) => void }) {
  const genres = useQuery({
    queryKey: ["discover-genres", library?.category_id, search.kind],
    queryFn: () => ok(api.GET("/discover/genres", { params: { query: { category_id: library!.category_id, kind: search.kind } } })),
    enabled: library !== undefined,
  });
  const kinds = [...new Set((library?.feeds ?? []).map((feed) => feed.kind).filter((kind) => kind !== ""))];
  const types = [...new Set((library ? [library] : libraries).flatMap((entry) => entry.types))];
  return (
    <>
      <ToolbarSelect label={t("discover.list")} value={search.list ?? ""} onChange={(value) => change({ list: value || undefined })}>
        <option value="">{t("discover.allLists")}</option>
        {types.map((type) => <option key={type} value={type}>{t(LISTS[type] ?? "discover.list.trending")}</option>)}
      </ToolbarSelect>
      {kinds.length > 1 && (
        <ToolbarSelect label={t("discover.kind")} value={search.kind ?? ""} onChange={(value) => change({ kind: value || undefined })}>
          <option value="">{t("discover.allKinds")}</option>
          {kinds.map((kind) => <option key={kind} value={kind}>{tOr(`discover.kind.${kind}`, kind)}</option>)}
        </ToolbarSelect>
      )}
      {library && (
        <ToolbarSelect label={t("discover.genre")} value={search.genre ?? ""} onChange={(value) => change({ genre: value || undefined })}>
          <option value="">{t("discover.allGenres")}</option>
          {(genres.data ?? []).map((genre) => <option key={genre.genre} value={genre.genre}>{genre.genre} ({genre.items})</option>)}
        </ToolbarSelect>
      )}
      <ToolbarSearch value={search.q ?? ""} onChange={(value) => change({ q: value || undefined })} placeholder={t("discover.search")} label={t("discover.search")} />
    </>
  );
}

function Wall({ libraries, library, search }: { libraries: DiscoverLibrary[]; library: DiscoverLibrary | undefined; search: DiscoverSearch }) {
  const queryClient = useQueryClient();
  const [adding, setAdding] = useState<Recommendation>();
  const [scroller, setScroller] = useState<HTMLDivElement | null>(null);
  const [width, setWidth] = useState(0);
  const { dismiss } = useRecommendationDismissal();
  const pages = useInfiniteQuery({
    queryKey: ["discover-recommendations", library?.category_id, search.list, search.kind, search.genre, search.q],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) =>
      ok(api.GET("/discover/recommendations", {
        params: { query: { category_id: library?.category_id, type: search.list as never, kind: search.kind, genre: search.genre, q: search.q, cursor: pageParam, limit: 40 } },
      })),
    getNextPageParam: (last) => last.next_cursor,
  });
  const refresh = () => void queryClient.invalidateQueries({ queryKey: ["discover-recommendations"] });
  const monitor = useMutation({
    meta: { handlesErrors: true },
    mutationFn: (workId: number) => ok(api.POST("/works/{id}/monitor", { params: { path: { id: workId } }, body: { monitored: true } })),
    onSuccess: () => {
      refresh();
      void queryClient.invalidateQueries({ queryKey: ["discover-dashboard"] });
    },
  });
  // The next page past what a refresh kept comes from the provider; only one library's feeds can be read further.
  const more = useMutation({
    meta: { handlesErrors: true },
    mutationFn: () => ok(api.POST("/discover/more", { body: { category_id: library!.category_id, type: search.list as never } })),
    onSuccess: () => {
      refresh();
      void queryClient.invalidateQueries({ queryKey: ["discover-libraries"] });
    },
  });
  useEffect(() => {
    if (!scroller) return;
    const observer = new ResizeObserver(([entry]) => setWidth(entry?.contentRect.width ?? 0));
    observer.observe(scroller);
    return () => observer.disconnect();
  }, [scroller]);
  const items = (pages.data?.pages ?? []).flatMap((page) => page.items);
  const columns = Math.max(1, Math.floor((width + GAP) / (CARD_MIN + GAP)));
  const cardWidth = width > 0 ? (width - GAP * (columns - 1)) / columns : CARD_MIN;
  const rowCount = Math.ceil(items.length / columns);
  const virtualizer = useVirtualizer({
    count: rowCount,
    getScrollElement: () => scroller,
    estimateSize: () => cardWidth * 1.5 + CARD_TEXT + GAP,
    overscan: 3,
  });
  const listed = (library?.types ?? []).filter((type) => !search.list || type === search.list);
  const canReadFurther = (library?.feeds ?? []).some((feed) => listed.includes(feed.type) && !feed.exhausted && feed.last_error === "");
  // Reading further is automatic: one page ahead of the last row on screen, from the pool first, then from the provider.
  const lastVisible = virtualizer.getVirtualItems().at(-1)?.index ?? -1;
  const nearEnd = rowCount > 0 && lastVisible >= rowCount - PREFETCH_ROWS;
  const providerDry = more.isSuccess && more.data.added === 0;
  useEffect(() => {
    if (!nearEnd) return;
    if (pages.hasNextPage && !pages.isFetchingNextPage) void pages.fetchNextPage();
    else if (!pages.hasNextPage && canReadFurther && !more.isPending && !more.isError && !providerDry) more.mutate();
  }, [nearEnd, pages, canReadFurther, more, providerDry, items.length]);
  if (!pages.isLoading && items.length === 0 && !canReadFurther) {
    return <EmptyState title={t("discover.empty")} hint={t("discover.emptyHint")} />;
  }
  return (
    <div ref={setScroller} className="relative flex-1 overflow-y-auto p-4">
      <ul className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
        {virtualizer.getVirtualItems().map((row) => (
          <li
            key={row.key}
            ref={virtualizer.measureElement}
            data-index={row.index}
            className="absolute top-0 left-0 grid w-full gap-4 pb-4"
            style={{ transform: `translateY(${row.start}px)`, gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}
          >
            {items.slice(row.index * columns, (row.index + 1) * columns).map((item) => {
              const own = libraries.find((entry) => entry.category_id === item.category_id);
              return (
                <RecommendationCard
                  key={item.id}
                  item={item}
                  libraryName={own?.name ?? ""}
                  canAdd={own?.can_add ?? false}
                  requestOnly={own?.request_only}
                  onAdd={() => setAdding(item)}
                  onMonitor={() => item.library_id && monitor.mutate(item.library_id)}
                  onDismiss={() => dismiss(item)}
                />
              );
            })}
          </li>
        ))}
      </ul>
      {(pages.hasNextPage || canReadFurther) && (
        <div className="mt-2 flex flex-col items-center gap-1">
          <Button
            busy={pages.isFetchingNextPage || more.isPending}
            onClick={() => (pages.hasNextPage ? void pages.fetchNextPage() : more.mutate())}
          >
            {t("discover.more")}
          </Button>
          {more.isSuccess && more.data.added === 0 && <p className="text-xs text-ink-muted">{t("discover.nothingMore")}</p>}
          {more.isError && <p className="text-xs text-danger">{t("discover.moreFailed")}</p>}
        </div>
      )}
      {adding && (
        <AddToLibrary
          candidate={{ provider: adding.provider, external_id: adding.external_id, title: adding.title, poster_url: adding.poster_url, year: adding.year, kind: adding.kind }}
          categoryId={String(adding.category_id)}
          onDone={() => {
            setAdding(undefined);
            refresh();
          }}
        />
      )}
    </div>
  );
}

/** One recommendation: poster, title, what the source says of it, the library it would go to, and the two things to do
 *  with it, always in view so they work by touch as well as by mouse. */
export function RecommendationCard({
  item,
  libraryName,
  canAdd,
  requestOnly = false,
  onAdd,
  onMonitor = () => {},
  onDismiss,
}: {
  item: Recommendation;
  libraryName: string;
  canAdd: boolean;
  requestOnly?: boolean;
  onAdd: () => void;
  onMonitor?: () => void;
  onDismiss: () => void;
}) {
  return (
    <article className="flex min-w-0 flex-col gap-1 text-sm">
      {item.poster_url ? (
        <img src={remoteImage(item.poster_url, "small")} alt="" loading="lazy" className="aspect-[2/3] w-full rounded-lg border border-border-subtle bg-surface object-cover" />
      ) : (
        <div aria-label={item.title} className="flex aspect-[2/3] w-full items-center justify-center rounded-lg border border-border-subtle bg-surface text-2xl text-ink-muted">
          {item.title.trim().charAt(0).toUpperCase() || "?"}
        </div>
      )}
      <h3 className="truncate font-medium" title={item.title}>
        {item.library_id ? item.title : <ExternalTitle categoryId={item.category_id} provider={item.provider} externalId={item.external_id} title={item.title} kind={item.kind} />}
      </h3>
      <p className="truncate text-xs text-ink-muted">
        {[item.year > 0 ? item.year : "", item.rating > 0 ? `${item.rating.toFixed(1)} (${item.votes})` : "", item.subtitle].filter(Boolean).join(" · ")}
      </p>
      {libraryName && <p className="truncate text-xs text-ink-muted" title={libraryName}>{t("discover.addsTo", { library: libraryName })}</p>}
      <div className="flex items-center gap-1">
        {item.library_id && requestOnly && (
          <Link to="/works/$id" params={{ id: String(item.library_id) }} className="flex-1 px-1 text-xs text-accent">{t("discover.inLibrary")}</Link>
        )}
        {item.library_id && !requestOnly && (
          <Button variant="primary" className="flex-1" icon={<Plus aria-hidden className="size-3.5" />} disabled={!canAdd} onClick={onMonitor}>{t("discover.monitor")}</Button>
        )}
        {!item.library_id && (
          <Button variant="primary" className="flex-1" icon={<Plus aria-hidden className="size-3.5" />} disabled={!canAdd} onClick={onAdd}>
            {requestOnly ? t("discover.request") : t("discover.add")}
          </Button>
        )}
        <Button variant="secondary" className="px-2" aria-label={t("discover.dismiss")} title={t("discover.dismiss")} onClick={onDismiss}>
          <EyeOff aria-hidden className="size-4" />
        </Button>
      </div>
      <div className="flex flex-wrap gap-1">
        {item.types.map((type) => <Chip key={type}>{t(LISTS[type] ?? "discover.list.trending")}</Chip>)}
      </div>
    </article>
  );
}
