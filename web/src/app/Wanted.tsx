import { useEffect, useState } from "react";

import { Link, useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, ListFilter, Search } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { useCanRequest } from "@/lib/useLibraryPermission";

import { InteractiveSearch } from "./InteractiveSearch";

type Work = components["schemas"]["Work"];
type Sort = "title" | "artist" | "year" | "searched";

const STATE_LABEL: Record<string, string> = {
  MISSING: "Missing",
  PARTIAL: "Partial",
  UPGRADABLE: "Upgradable",
  COMPLETE: "Complete",
};

const PAGE_SIZES = [25, 50, 100] as const;

const COLUMNS: readonly { sort: Sort; label: string; width: string }[] = [
  { sort: "artist", label: "Artist", width: "1fr" },
  { sort: "title", label: "Album", width: "1.4fr" },
  { sort: "year", label: "Released", width: "6rem" },
  { sort: "searched", label: "Last searched", width: "10rem" },
];

const GRID = "grid grid-cols-[1fr_1.4fr_6rem_10rem_6rem_14rem] items-center gap-3 px-3";

/** Lidarr's Wanted -> Missing: sortable by header, paged, searched one album, one page or the whole
 *  library at a time. Search sends the best release; the list icon lets a person choose it. */
export function Wanted() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const search = useSearch({ strict: false }) as { sort?: Sort; dir?: "asc" | "desc"; size?: number };
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const sort: Sort = search.sort ?? "artist";
  const dir = search.dir ?? "asc";
  const size = PAGE_SIZES.find((candidate) => candidate === Number(search.size)) ?? 50;

  // Cursor paging (never OFFSET): the cursor that opened each page seen so far.
  const [cursors, setCursors] = useState<(string | undefined)[]>([undefined]);
  const page = cursors.length - 1;
  useEffect(() => setCursors([undefined]), [categoryId, sort, dir, size]);

  const works = useQuery({
    queryKey: ["wanted", categoryId, sort, dir, size, cursors[page]],
    queryFn: async () => {
      const { data, error } = await api.GET("/works", {
        params: {
          query: {
            cursor: cursors[page], limit: size, sort, dir, wanted: true,
            category_id: categoryId ? Number(categoryId) : undefined,
          },
        },
      });
      if (error) throw error;
      return data;
    },
  });

  // The total comes from the library's counter, as every count does (section 21.12).
  const libraries = useQuery({
    queryKey: ["libraries"],
    queryFn: async () => {
      const { data, error } = await api.GET("/libraries");
      if (error) throw error;
      return data;
    },
  });
  const total = libraries.data?.find((library) => String(library.id) === categoryId)?.wanted;
  const pages = total === undefined ? undefined : Math.max(1, Math.ceil(total / size));

  const canRequest = useCanRequest((library) => String(library.id) === categoryId);

  const setView = (next: { sort?: Sort; dir?: "asc" | "desc"; size?: number }) =>
    void navigate({ to: ".", search: { sort, dir, size, ...next }, replace: true });

  const [rowResult, setRowResult] = useState<Record<number, string>>({});
  const grab = useMutation({
    mutationFn: async (workId: number) => {
      const { data, error } = await api.POST("/works/{id}/grab", { params: { path: { id: workId } } });
      if (error) throw error;
      return data;
    },
    onMutate: (workId) => setRowResult((held) => ({ ...held, [workId]: "Searching…" })),
    onSuccess: (data, workId) => {
      const message = data.already_downloading
        ? "Already downloading"
        : data.sent
          ? "Sent to the download client"
          : data.found > 0
            ? `${data.found} found, none acceptable`
            : "Nothing found";
      setRowResult((held) => ({ ...held, [workId]: message }));
      void queryClient.invalidateQueries({ queryKey: ["wanted"] });
    },
    onError: (_, workId) =>
      setRowResult((held) => ({ ...held, [workId]: "Search failed -- is an indexer connected?" })),
  });

  const searchLibrary = useMutation({
    mutationFn: async (workIds?: number[]) => {
      if (!categoryId) return undefined;
      const { data, error } = await api.POST("/categories/{id}/search", {
        params: { path: { id: Number(categoryId) } },
        body: workIds ? { work_ids: workIds } : undefined,
      });
      if (error) throw error;
      return data;
    },
  });

  const [interactive, setInteractive] = useState<Work | null>(null);
  const items = works.data?.items ?? [];
  const nextCursor = works.data?.next_cursor;

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-3 border-b border-border-subtle px-4 py-2">
        <h2 className="text-sm font-medium">Wanted</h2>
        <span className="text-xs text-ink-muted">Monitored and not yet complete.</span>
        <span className="ml-auto flex items-center gap-2 text-xs text-ink-muted">
          {searchLibrary.isSuccess &&
            (searchLibrary.data?.duplicate ? "That search is already running." : "Searching in the background.")}
          {searchLibrary.isError && <span className="text-danger">The search could not start.</span>}
          {!canRequest && <span>Searching needs request access to this library.</span>}
          {canRequest && (
          <>
          <button
            type="button"
            disabled={searchLibrary.isPending || items.length === 0}
            onClick={() => searchLibrary.mutate(items.map((work) => work.id))}
            className={toolbarButton}
          >
            <Search aria-hidden className="size-3.5" />
            Search this page
          </button>
          <button
            type="button"
            disabled={searchLibrary.isPending || items.length === 0}
            onClick={() => searchLibrary.mutate(undefined)}
            className={toolbarButton}
          >
            <Search aria-hidden className="size-3.5" />
            Search all
          </button>
          </>
          )}
        </span>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div role="row" className={`${GRID} sticky top-0 border-b border-border-subtle bg-surface py-2 text-xs font-medium text-ink-muted`}>
          {COLUMNS.map((column) => (
            <button
              key={column.sort}
              type="button"
              onClick={() => setView({ sort: column.sort, dir: sort === column.sort && dir === "asc" ? "desc" : "asc" })}
              className="flex items-center gap-1 text-left hover:text-ink"
            >
              {column.label}
              {sort === column.sort &&
                (dir === "asc" ? <ArrowUp aria-hidden className="size-3" /> : <ArrowDown aria-hidden className="size-3" />)}
            </button>
          ))}
          <span />
          <span />
        </div>

        {works.isLoading && <Message>Loading…</Message>}
        {works.isError && <Message>Wanted items could not be read.</Message>}
        {works.isSuccess && items.length === 0 && page === 0 && (
          <Message>Nothing wanted -- every monitored album in this library is complete.</Message>
        )}
        {items.map((work) => (
          <div key={work.id} role="row" className={`${GRID} border-b border-border-subtle py-1.5 text-sm`}>
            <span className="truncate">{work.artist ?? ""}</span>
            <Link to="/works/$id" params={{ id: String(work.id) }} className="truncate hover:underline">
              {work.title}
            </Link>
            <span className="text-ink-muted">{work.year ?? ""}</span>
            <span className="text-xs text-ink-muted">
              {work.last_searched_at ? new Date(work.last_searched_at).toLocaleString(undefined, { hour12: false }) : "Never"}
            </span>
            <StateBadge state={work.wanted_state} />
            <span className="flex items-center justify-end gap-2 text-xs text-ink-muted">
              <span className="truncate">{rowResult[work.id]}</span>
              {canRequest && (
              <>
              <button
                type="button"
                title="Search: send the best release"
                aria-label={`Search for ${work.title}`}
                disabled={grab.isPending && grab.variables === work.id}
                onClick={() => grab.mutate(work.id)}
                className="rounded-md border border-border-subtle p-1 hover:text-ink disabled:opacity-50"
              >
                <Search aria-hidden className="size-3.5" />
              </button>
              <button
                type="button"
                title="Interactive search: choose the release"
                aria-label={`Interactive search for ${work.title}`}
                onClick={() => setInteractive(work)}
                className="rounded-md border border-border-subtle p-1 hover:text-ink"
              >
                <ListFilter aria-hidden className="size-3.5" />
              </button>
              </>
              )}
            </span>
          </div>
        ))}
      </div>

      <div className="flex shrink-0 items-center gap-3 border-t border-border-subtle px-4 py-2 text-xs text-ink-muted">
        <button
          type="button"
          disabled={page === 0}
          onClick={() => setCursors((held) => held.slice(0, -1))}
          className={toolbarButton}
        >
          Previous
        </button>
        <span>
          Page {page + 1}
          {pages !== undefined && ` of ${pages}`}
          {total !== undefined && ` · ${total.toLocaleString()} ${total === 1 ? "album" : "albums"}`}
        </span>
        <button
          type="button"
          disabled={!nextCursor}
          onClick={() => setCursors((held) => [...held, nextCursor])}
          className={toolbarButton}
        >
          Next
        </button>
        <label className="ml-auto flex items-center gap-1.5">
          Per page
          <select
            value={size}
            onChange={(event) => setView({ size: Number(event.target.value) })}
            className="rounded-md border border-border-subtle bg-canvas px-1.5 py-0.5"
          >
            {PAGE_SIZES.map((option) => (
              <option key={option} value={option}>
                {option}
              </option>
            ))}
          </select>
        </label>
      </div>

      {interactive && (
        <InteractiveSearch workId={interactive.id} title={interactive.title} onClose={() => setInteractive(null)} />
      )}
    </div>
  );
}

function StateBadge({ state }: { state?: string }) {
  if (!state) return <span />;
  return (
    <span
      className={`w-fit rounded-full border px-1.5 py-px text-[0.6875rem] ${
        state === "MISSING" ? "border-danger text-danger" : "border-warn text-warn"
      }`}
    >
      {STATE_LABEL[state] ?? state}
    </span>
  );
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}

const toolbarButton =
  "flex items-center gap-1 rounded-md border border-border-subtle px-2 py-1 hover:text-ink disabled:opacity-50";
