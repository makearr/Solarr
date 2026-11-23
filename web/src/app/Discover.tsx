import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { Plus, X } from "lucide-react";
import { useState } from "react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t, type MessageKey } from "@/lib/i18n";
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

type DiscoverSearch = { library?: string; list?: string; genre?: string; q?: string };

/** What the libraries do not hold yet, from their sources' charts, one library at a time. */
export function Discover() {
  const search = useSearch({ strict: false }) as DiscoverSearch;
  const navigate = useNavigate();
  const libraries = useDiscoverLibraries();
  const change = (patch: DiscoverSearch) =>
    void navigate({ to: "/discover", search: ((previous: DiscoverSearch) => ({ ...previous, ...patch })) as never });
  const all = libraries.data ?? [];
  const current = all.find((library) => String(library.category_id) === search.library) ?? all[0];
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
                onClick={() => change({ library: String(library.category_id), genre: undefined })}
                className={`rounded-md px-2 py-1 ${library.category_id === current.category_id ? "bg-accent/10 text-ink" : "text-ink-muted hover:text-ink"}`}
              >
                {library.name}
              </button>
            ))}
          </div>
        )}
      >
        <Filters library={current} search={search} change={change} />
      </Toolbar>
      <Wall library={current} search={search} />
    </div>
  );
}

function Filters({ library, search, change }: { library: DiscoverLibrary; search: DiscoverSearch; change: (patch: DiscoverSearch) => void }) {
  const genres = useQuery({
    queryKey: ["discover-genres", library.category_id],
    queryFn: () => ok(api.GET("/discover/genres", { params: { query: { category_id: library.category_id } } })),
  });
  return (
    <>
      <ToolbarSelect label={t("discover.list")} value={search.list ?? ""} onChange={(value) => change({ list: value || undefined })}>
        <option value="">{t("discover.allLists")}</option>
        {library.types.map((type) => <option key={type} value={type}>{t(LISTS[type] ?? "discover.list.trending")}</option>)}
      </ToolbarSelect>
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
  const pages = useInfiniteQuery({
    queryKey: ["discover-recommendations", library.category_id, search.list, search.genre, search.q],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) =>
      ok(api.GET("/discover/recommendations", {
        params: { query: { category_id: library.category_id, type: search.list as never, genre: search.genre, q: search.q, cursor: pageParam, limit: 40 } },
      })),
    getNextPageParam: (last) => last.next_cursor,
  });
  const refresh = () => void queryClient.invalidateQueries({ queryKey: ["discover-recommendations"] });
  const dismiss = useMutation({
    meta: { handlesErrors: true },
    mutationFn: (id: number) => ok(api.POST("/discover/recommendations/{id}/dismiss", { params: { path: { id } } })),
    onSuccess: () => {
      refresh();
      void queryClient.invalidateQueries({ queryKey: ["discover-dashboard"] });
    },
  });
  const items = (pages.data?.pages ?? []).flatMap((page) => page.items);
  if (!pages.isLoading && items.length === 0) {
    return <EmptyState title={t("discover.empty")} hint={t("discover.emptyHint")} />;
  }
  return (
    <div className="flex-1 overflow-y-auto p-4">
      <ul className="grid grid-cols-[repeat(auto-fill,minmax(10rem,1fr))] gap-4">
        {items.map((item) => (
          <li key={item.id}>
            <RecommendationCard item={item} canAdd={library.can_add} onAdd={() => setAdding(item)} onDismiss={() => dismiss.mutate(item.id)} />
          </li>
        ))}
      </ul>
      {pages.hasNextPage && (
        <div className="mt-4 flex justify-center">
          <Button busy={pages.isFetchingNextPage} onClick={() => void pages.fetchNextPage()}>{t("discover.more")}</Button>
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
export function RecommendationCard({ item, canAdd, onAdd, onDismiss }: { item: Recommendation; canAdd: boolean; onAdd: () => void; onDismiss: () => void }) {
  return (
    <article className="group flex flex-col gap-1 text-sm">
      <div className="relative">
        {item.poster_url ? (
          <img src={remoteImage(item.poster_url, "small")} alt="" loading="lazy" className="aspect-[2/3] w-full rounded-lg border border-border-subtle bg-surface object-cover" />
        ) : (
          <div className="aspect-[2/3] w-full rounded-lg border border-border-subtle bg-surface" />
        )}
        <div className="absolute inset-x-0 bottom-0 flex justify-between gap-1 bg-canvas/80 p-1 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100">
          {item.library_id ? (
            <Link to="/works/$id" params={{ id: String(item.library_id) }} className="px-1 text-xs text-accent">{t("discover.inLibrary")}</Link>
          ) : (
            <Button variant="primary" icon={<Plus aria-hidden className="size-3.5" />} disabled={!canAdd} onClick={onAdd}>{t("discover.add")}</Button>
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
