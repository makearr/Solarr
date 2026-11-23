import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { Plus, Undo2, X } from "lucide-react";
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
const CARD_TEXT = 92;

/** What the libraries do not hold yet, from their sources' charts, one library at a time. */
export function Discover() {
  const search = useSearch({ strict: false }) as DiscoverSearch;
  const navigate = useNavigate();
  const libraries = useDiscoverLibraries();
  const change = (patch: DiscoverSearch) =>
    void navigate({ to: "/discover", search: ((previous: DiscoverSearch) => ({ ...previous, ...patch })) as never });
  const all = libraries.data ?? [];
  const current = all.find((library) => String(library.category_id) === searchLibrary(search)) ?? all[0];
  if (libraries.isLoading) return <p className="p-6 text-sm text-ink-muted">{t("discover.loading")}</p>;
  if (!current) return <EmptyState title={t("discover.nothing")} hint={t("discover.nothingHint")} />;
  return (
    <div className="flex h-full flex-col">
      <Toolbar
        title={t("discover.page")}
        chips={all.length > 1 && (
          <div role="tablist" className="flex gap-1">
            {all.map((library) => (
              <button
                key={library.category_id}
                role="tab"
                aria-selected={library.category_id === current.category_id}
                onClick={() => change({ library: library.category_id, genre: undefined, kind: undefined })}
                className={`rounded-md px-2 py-1 ${library.category_id === current.category_id ? "bg-accent/10 text-ink" : "text-ink-muted hover:text-ink"}`}
              >
                {tabName(library, all)}
              </button>
            ))}
          </div>
        )}
      >
        <ToolbarHidden active={searchHidden(search)} onChange={(on) => change({ hidden: on ? 1 : undefined })} />
        {!searchHidden(search) && <Filters library={current} search={search} change={change} />}
      </Toolbar>
      {searchHidden(search) ? <HiddenList /> : <Wall library={current} search={search} />}
    </div>
  );
}

/** Two libraries of different types may share a name; the type tells them apart. */
function tabName(library: DiscoverLibrary, all: DiscoverLibrary[]) {
  const same = all.filter((other) => other.name === library.name).length > 1;
  return same ? `${library.name} (${library.media_type})` : library.name;
}

function ToolbarHidden({ active, onChange }: { active: boolean; onChange: (active: boolean) => void }) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={() => onChange(!active)}
      className={`rounded-md border border-border-subtle px-2 py-1 ${active ? "bg-accent/10 text-ink" : "text-ink-muted hover:text-ink"}`}
    >
      {t("discover.hiddenView")}
    </button>
  );
}

/** What this person said they are not interested in, each with a way back. */
function HiddenList() {
  const queryClient = useQueryClient();
  const hidden = useQuery({ queryKey: ["discover-dismissals"], queryFn: () => ok(api.GET("/discover/dismissals")) });
  const restore = useMutation({
    meta: { handlesErrors: true },
    mutationFn: (item: components["schemas"]["RecommendationDismissal"]) =>
      ok(api.DELETE("/discover/dismissals/{provider}/{external_id}", {
        params: { path: { provider: item.provider, external_id: item.external_id }, query: { kind: item.kind } },
      })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["discover-dismissals"] });
      void queryClient.invalidateQueries({ queryKey: ["discover-recommendations"] });
      void queryClient.invalidateQueries({ queryKey: ["discover-dashboard"] });
    },
  });
  if (hidden.data?.length === 0) return <EmptyState title={t("discover.noHidden")} hint={t("discover.noHiddenHint")} />;
  return (
    <ul className="flex-1 overflow-y-auto p-4 text-sm">
      {(hidden.data ?? []).map((item) => (
        <li key={`${item.kind}:${item.provider}:${item.external_id}`} className="flex items-center gap-3 border-b border-border-subtle py-2">
          <span className="min-w-0 flex-1 truncate">{item.title}</span>
          <span className="text-xs text-ink-muted">{item.media_type}</span>
          <Button variant="ghost" icon={<Undo2 aria-hidden className="size-3.5" />} busy={restore.isPending && restore.variables === item} onClick={() => restore.mutate(item)}>
            {t("discover.restore")}
          </Button>
        </li>
      ))}
    </ul>
  );
}

function Filters({ library, search, change }: { library: DiscoverLibrary; search: DiscoverSearch; change: (patch: DiscoverSearch) => void }) {
  const genres = useQuery({
    queryKey: ["discover-genres", library.category_id, search.kind],
    queryFn: () => ok(api.GET("/discover/genres", { params: { query: { category_id: library.category_id, kind: search.kind } } })),
  });
  const kinds = [...new Set(library.feeds.map((feed) => feed.kind).filter((kind) => kind !== ""))];
  return (
    <>
      <ToolbarSelect label={t("discover.list")} value={search.list ?? ""} onChange={(value) => change({ list: value || undefined })}>
        <option value="">{t("discover.allLists")}</option>
        {library.types.map((type) => <option key={type} value={type}>{t(LISTS[type] ?? "discover.list.trending")}</option>)}
      </ToolbarSelect>
      {kinds.length > 1 && (
        <ToolbarSelect label={t("discover.kind")} value={search.kind ?? ""} onChange={(value) => change({ kind: value || undefined })}>
          <option value="">{t("discover.allKinds")}</option>
          {kinds.map((kind) => <option key={kind} value={kind}>{tOr(`discover.kind.${kind}`, kind)}</option>)}
        </ToolbarSelect>
      )}
      <ToolbarSelect label={t("discover.genre")} value={search.genre ?? ""} onChange={(value) => change({ genre: value || undefined })}>
        <option value="">{t("discover.allGenres")}</option>
        {(genres.data ?? []).map((genre) => <option key={genre.genre} value={genre.genre}>{genre.genre} ({genre.items})</option>)}
      </ToolbarSelect>
      <ToolbarSearch value={search.q ?? ""} onChange={(value) => change({ q: value || undefined })} placeholder={t("discover.search")} label={t("discover.search")} />
    </>
  );
}

function Wall({ library, search }: { library: DiscoverLibrary; search: DiscoverSearch }) {
  const queryClient = useQueryClient();
  const [adding, setAdding] = useState<Recommendation>();
  const [undo, setUndo] = useState<Recommendation>();
  const [scroller, setScroller] = useState<HTMLDivElement | null>(null);
  const [width, setWidth] = useState(0);
  const pages = useInfiniteQuery({
    queryKey: ["discover-recommendations", library.category_id, search.list, search.kind, search.genre, search.q],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) =>
      ok(api.GET("/discover/recommendations", {
        params: { query: { category_id: library.category_id, type: search.list as never, kind: search.kind, genre: search.genre, q: search.q, cursor: pageParam, limit: 40 } },
      })),
    getNextPageParam: (last) => last.next_cursor,
  });
  const refresh = () => void queryClient.invalidateQueries({ queryKey: ["discover-recommendations"] });
  const dismiss = useMutation({
    meta: { handlesErrors: true },
    mutationFn: (item: Recommendation) => ok(api.POST("/discover/recommendations/{id}/dismiss", { params: { path: { id: item.id } } })),
    onSuccess: (_, item) => {
      setUndo(item);
      refresh();
      void queryClient.invalidateQueries({ queryKey: ["discover-dashboard"] });
      void queryClient.invalidateQueries({ queryKey: ["discover-dismissals"] });
    },
  });
  const restore = useMutation({
    meta: { handlesErrors: true },
    mutationFn: (item: Recommendation) =>
      ok(api.DELETE("/discover/dismissals/{provider}/{external_id}", { params: { path: { provider: item.provider, external_id: item.external_id }, query: { kind: item.kind } } })),
    onSuccess: () => {
      setUndo(undefined);
      refresh();
      void queryClient.invalidateQueries({ queryKey: ["discover-dashboard"] });
      void queryClient.invalidateQueries({ queryKey: ["discover-dismissals"] });
    },
  });
  const monitor = useMutation({
    meta: { handlesErrors: true },
    mutationFn: (workId: number) => ok(api.POST("/works/{id}/monitor", { params: { path: { id: workId } }, body: { monitored: true } })),
    onSuccess: () => {
      refresh();
      void queryClient.invalidateQueries({ queryKey: ["discover-dashboard"] });
    },
  });
  // The next page past what a refresh kept comes from the provider.
  const more = useMutation({
    meta: { handlesErrors: true },
    mutationFn: () => ok(api.POST("/discover/more", { body: { category_id: library.category_id, type: search.list as never } })),
    onSuccess: () => {
      refresh();
      void queryClient.invalidateQueries({ queryKey: ["discover-libraries"] });
    },
  });
  useEffect(() => {
    if (!undo) return;
    const timer = window.setTimeout(() => setUndo(undefined), 8000);
    return () => window.clearTimeout(timer);
  }, [undo]);
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
  const listed = library.types.filter((type) => !search.list || type === search.list);
  const canReadFurther = library.feeds.some((feed) => listed.includes(feed.type) && !feed.exhausted && feed.last_error === "");
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
            {items.slice(row.index * columns, (row.index + 1) * columns).map((item) => (
              <RecommendationCard
                key={item.id}
                item={item}
                canAdd={library.can_add}
                requestOnly={library.request_only}
                onAdd={() => setAdding(item)}
                onMonitor={() => item.library_id && monitor.mutate(item.library_id)}
                onDismiss={() => dismiss.mutate(item)}
              />
            ))}
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
      {undo && (
        <div role="status" className="fixed bottom-4 left-1/2 z-30 flex -translate-x-1/2 items-center gap-3 rounded-md border border-border-subtle bg-canvas px-3 py-2 text-sm shadow-lg">
          <span>{t("discover.hidden", { title: undo.title })}</span>
          <Button variant="ghost" icon={<Undo2 aria-hidden className="size-3.5" />} busy={restore.isPending} onClick={() => restore.mutate(undo)}>
            {t("discover.undo")}
          </Button>
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

/** One recommendation: poster, title, what the source says of it, and the two things to do with it. */
export function RecommendationCard({
  item,
  canAdd,
  requestOnly = false,
  onAdd,
  onMonitor = () => {},
  onDismiss,
}: {
  item: Recommendation;
  canAdd: boolean;
  requestOnly?: boolean;
  onAdd: () => void;
  onMonitor?: () => void;
  onDismiss: () => void;
}) {
  return (
    <article className="group flex min-w-0 flex-col gap-1 text-sm">
      <div className="relative">
        {item.poster_url ? (
          <img src={remoteImage(item.poster_url, "small")} alt="" loading="lazy" className="aspect-[2/3] w-full rounded-lg border border-border-subtle bg-surface object-cover" />
        ) : (
          <div aria-label={item.title} className="flex aspect-[2/3] w-full items-center justify-center rounded-lg border border-border-subtle bg-surface text-2xl text-ink-muted">
            {item.title.trim().charAt(0).toUpperCase() || "?"}
          </div>
        )}
        <div className="absolute inset-x-0 bottom-0 flex justify-between gap-1 bg-canvas/80 p-1 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100">
          {item.library_id && requestOnly && (
            <Link to="/works/$id" params={{ id: String(item.library_id) }} className="px-1 text-xs text-accent">{t("discover.inLibrary")}</Link>
          )}
          {item.library_id && !requestOnly && (
            <Button variant="primary" icon={<Plus aria-hidden className="size-3.5" />} disabled={!canAdd} onClick={onMonitor}>{t("discover.monitor")}</Button>
          )}
          {!item.library_id && (
            <Button variant="primary" icon={<Plus aria-hidden className="size-3.5" />} disabled={!canAdd} onClick={onAdd}>
              {requestOnly ? t("discover.request") : t("discover.add")}
            </Button>
          )}
          <Button variant="icon" aria-label={t("discover.dismiss")} title={t("discover.dismiss")} onClick={onDismiss}><X aria-hidden className="size-4" /></Button>
        </div>
      </div>
      <h3 className="truncate font-medium" title={item.title}>{item.title}</h3>
      <p className="truncate text-xs text-ink-muted">
        {[item.year > 0 ? item.year : "", item.rating > 0 ? `${item.rating.toFixed(1)} (${item.votes})` : "", item.subtitle].filter(Boolean).join(" · ")}
      </p>
      <div className="flex flex-wrap gap-1">
        {item.types.map((type) => <Chip key={type}>{t(LISTS[type] ?? "discover.list.trending")}</Chip>)}
      </div>
    </article>
  );
}
