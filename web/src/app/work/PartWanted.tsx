import { useEffect, useRef, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Search } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";

import { vocabularyOf } from "@/lib/i18n";
import type { MediaTypeInfo } from "@/lib/useMediaTypes";
import { Chip } from "@/ui/Chip";

import { InteractiveSearch } from "../InteractiveSearch";
import { grabNote } from "./grabNote";
import { fileChips } from "./workFormat";

type Part = components["schemas"]["WantedPart"];

type View = "missing" | "cutoff";

/** Sonarr's Wanted for a type wanted part by part: Missing (monitored, out, no file) and Cutoff unmet (files below the
 *  library's target or cutoff). A search covers the part's whole group, so a pack can fill several rows at once. */
export function PartWanted({ info }: { info: MediaTypeInfo | undefined }) {
  const words = vocabularyOf(info);
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const [view, setView] = useState<View>("missing");
  const queryClient = useQueryClient();
  const [dialog, setDialog] = useState<Part | null>(null);
  const keyOf = (part: Part) => `${part.work_id}:${part.group_ref}`;
  const [notes, setNotes] = useState<Record<string, string>>({});

  const pages = useInfiniteQuery({
    queryKey: ["wanted", "parts", categoryId, view],
    initialPageParam: "",
    queryFn: ({ pageParam }) => ok(api.GET("/wanted/parts", {
      params: { query: { category_id: Number(categoryId), limit: 100, view, ...(pageParam ? { cursor: pageParam } : {}) } },
    })),
    getNextPageParam: (last) => last.next_cursor || undefined,
  });
  const rows = (pages.data?.pages ?? []).flatMap((page) => page.items);
  const judged = pages.data?.pages[0]?.judged !== false;

  const grab = useMutation({
    mutationFn: async (part: Part) => {
      const { data, error } = await api.POST("/works/{id}/grab", { params: { path: { id: part.work_id }, query: { group: part.group_ref } } });
      if (error) throw error;
      return { part, data };
    },
    onSuccess: ({ part, data }) => {
      setNotes((current) => ({ ...current, [keyOf(part)]: grabNote(data) }));
      void queryClient.invalidateQueries({ queryKey: ["wanted", "parts", categoryId] });
    },
    onError: (_error, part) => setNotes((current) => ({ ...current, [keyOf(part)]: "Search failed: is an indexer connected?" })),
  });

  const [sweep, setSweep] = useState<{ done: number; total: number } | null>(null);
  const searchListed = async () => {
    const seasons = new Map<string, Part>();
    for (const episode of rows) seasons.set(keyOf(episode), episode);
    const queue = [...seasons.values()];
    for (const [index, episode] of queue.entries()) {
      setSweep({ done: index, total: queue.length });
      try {
        await grab.mutateAsync(episode);
      } catch {
        // the row's own note says it failed; the sweep goes on
      }
    }
    setSweep(null);
  };

  const sentinel = useRef<HTMLDivElement>(null);
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = pages;
  useEffect(() => {
    const element = sentinel.current;
    if (!element || !hasNextPage) return;
    const observer = new IntersectionObserver((entries) => {
      if (entries[0]?.isIntersecting && !isFetchingNextPage) void fetchNextPage();
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage, rows.length]);

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-3 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="mr-2 text-sm font-medium">Wanted</h1>
        {(["missing", "cutoff"] as const).map((key) => (
          <button
            key={key}
            type="button"
            aria-pressed={view === key}
            onClick={() => setView(key)}
            className={`rounded-full border px-3 py-1 text-xs ${view === key ? "border-accent bg-accent/10 text-ink" : "border-border-subtle text-ink-muted hover:text-ink"}`}
          >
            {key === "missing" ? "Missing" : "Cutoff unmet"}
          </button>
        ))}
        {rows.length > 0 && (view === "cutoff" || view === "missing") && (
          <button
            type="button"
            disabled={sweep !== null}
            onClick={() => void searchListed()}
            className="ml-auto flex items-center gap-1 rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted hover:text-ink disabled:opacity-50"
          >
            <Search aria-hidden className="size-3.5" />
            {sweep ? `Searching ${sweep.done + 1} of ${sweep.total}` : `Search listed ${words.groups}`}
          </button>
        )}
      </div>
      <p className="text-xs text-ink-muted">
        {view === "missing"
          ? `Monitored ${words.parts} that are out and not on disk. Search looks for the whole ${words.group}, so a pack can fill several rows at once.`
          : `${words.Parts} on disk whose file is below the library's target or cutoff (Quality page). Search looks for a strictly better release of the whole ${words.group}; Interactive picks one for one ${words.part}. The hunt also does this on its own, a few a day.`}
      </p>
      {view === "cutoff" && !judged && (
        <p className="text-sm text-ink-muted">
          This library has no target or cutoff yet, so nothing can be below it. Set one on its{" "}
          <Link to="/library/$categoryId/config/quality" params={{ categoryId }} className="text-accent underline">
            Quality page
          </Link>
          .
        </p>
      )}
      {pages.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {pages.error !== null && !pages.isLoading && <p className="text-sm text-danger">The list could not be read.</p>}
      {!pages.isLoading && rows.length === 0 && pages.error === null && (view === "missing" || judged) && (
        <p className="text-sm text-ink-muted">{view === "missing" ? "Nothing is missing." : "Every measured file meets the cutoff."}</p>
      )}
      <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
        {rows.map((part) => {
          const note = notes[keyOf(part)];
          return (
            <li key={part.part_id} className="flex flex-wrap items-center gap-3 px-3 py-2 text-sm">
              <Link to="/library/$categoryId/works/$workId" params={{ categoryId, workId: String(part.work_id) }} className="w-56 truncate font-medium hover:underline">
                {part.work_title}
              </Link>
              <span className="w-28 truncate text-ink-muted">
                {part.group_label} · {part.number}
              </span>
              <span className="min-w-0 flex-1 truncate">{part.title ?? "TBA"}</span>
              <span className="w-24 text-xs text-ink-muted">{part.date ?? ""}</span>
              {part.file && (
                <span className="flex flex-wrap gap-1">
                  {fileChips(part.file).slice(0, 3).map((chip) => (
                    <Chip key={chip}>{chip}</Chip>
                  ))}
                </span>
              )}
              {note && <span className="text-xs text-ink-muted">{note}</span>}
              <button
                type="button"
                title={view === "missing" ? `Search this ${words.group} and send the best release` : `Search this ${words.group} for a strictly better release`}
                aria-label={`Search ${part.work_title} ${part.group_label}`}
                onClick={() => grab.mutate(part)}
                className="text-ink-muted hover:text-ink"
              >
                <Search aria-hidden className="size-4" />
              </button>
              <button
                type="button"
                onClick={() => setDialog(part)}
                className="rounded-md border border-border-subtle px-2 py-0.5 text-xs text-ink-muted hover:text-ink"
              >
                Interactive
              </button>
            </li>
          );
        })}
      </ul>
      {pages.hasNextPage && (
        <div ref={sentinel} className="py-2 text-center text-xs text-ink-muted">
          {pages.isFetchingNextPage ? "Loading…" : ""}
        </div>
      )}
      {dialog && (
        <InteractiveSearch
          workId={dialog.work_id}
          scope={{ group: dialog.group_ref, part: view === "cutoff" ? dialog.part_id : undefined }}
          title={view === "cutoff" ? `${dialog.work_title} ${dialog.group_label} ${dialog.number}` : `${dialog.work_title}, ${dialog.group_label}`}
          onClose={() => setDialog(null)}
        />
      )}
    </div>
  );
}
