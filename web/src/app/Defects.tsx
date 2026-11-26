import { useRef, useState } from "react";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { useVirtualizer } from "@tanstack/react-virtual";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t, type MessageKey } from "@/lib/i18n";
import { invalidateWorkLists } from "@/lib/resources";
import { useLibrary } from "@/lib/useLibrary";
import { Button } from "@/ui/Button";
import { Chip } from "@/ui/Chip";
import { ConfirmDialog } from "@/ui/ConfirmDialog";
import { EmptyState } from "@/ui/EmptyState";

import { formatSize } from "./work/workFormat";

type Defect = components["schemas"]["DefectItem"];
type Action = "delete" | "rerequest";

const ROW_HEIGHT = 60;

/** Files the scan found broken or incomplete, with why; re-request them (recycle, wanted again, searched) or delete them. */
export function Defects() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const library = useLibrary(categoryId);
  const queryClient = useQueryClient();
  const scrollRef = useRef<HTMLDivElement>(null);
  const [chosen, setChosen] = useState<ReadonlySet<number>>(new Set());
  const [confirming, setConfirming] = useState<Action>();
  const list = useInfiniteQuery({
    queryKey: ["defects", categoryId],
    initialPageParam: "",
    queryFn: ({ pageParam }) =>
      ok(api.GET("/categories/{id}/defects", { params: { path: { id: Number(categoryId) }, query: { cursor: pageParam || undefined, limit: 100 } } })),
    getNextPageParam: (page) => page.next_cursor,
  });
  const rows: Defect[] = list.data?.pages.flatMap((page) => page.items) ?? [];
  const total = list.data?.pages[0]?.total ?? 0;
  const resolve = useMutation({
    mutationFn: (action: Action) => ok(api.POST("/categories/{id}/defects/resolve", { params: { path: { id: Number(categoryId) } }, body: { action, ids: [...chosen] } })),
    onSuccess: (result, action) => {
      setChosen(new Set());
      setConfirming(undefined);
      void queryClient.invalidateQueries({ queryKey: ["defects", categoryId] });
      invalidateWorkLists(queryClient);
      // Searching is best effort: the server refuses it where searching is stopped or not permitted.
      if (action === "rerequest") for (const id of result.work_ids) void api.POST("/works/{id}/grab", { params: { path: { id } } });
    },
  });
  const virtualizer = useVirtualizer({ count: rows.length, getScrollElement: () => scrollRef.current, estimateSize: () => ROW_HEIGHT, overscan: 8 });
  const toggle = (id: number) => setChosen((current) => { const next = new Set(current); if (!next.delete(id)) next.add(id); return next; });
  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-3 p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">{t("defects.title", { library: library?.name ?? "" })}</h1>
        <p className="text-sm text-ink-muted">{t("defects.intro")}</p>
      </header>
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <span className="tabular-nums text-ink-muted">{t("defects.count", { count: total })}</span>
        <Button disabled={rows.length === 0} onClick={() => setChosen(chosen.size === rows.length ? new Set() : new Set(rows.map((row) => row.media_object_id)))}>
          {chosen.size === rows.length && rows.length > 0 ? t("defects.selectNone") : t("defects.selectLoaded")}
        </Button>
        <span className="ml-auto flex gap-2">
          <Button disabled={chosen.size === 0} onClick={() => setConfirming("rerequest")}>{t("defects.rerequest", { count: chosen.size })}</Button>
          <Button variant="danger" disabled={chosen.size === 0} onClick={() => setConfirming("delete")}>{t("defects.delete", { count: chosen.size })}</Button>
        </span>
      </div>
      {list.isError && <p className="text-sm text-danger">{t("defects.failed")}</p>}
      {resolve.data && resolve.data.problems.length > 0 && (
        <ul className="text-xs text-warn">{resolve.data.problems.map((problem) => <li key={problem}>{problem}</li>)}</ul>
      )}
      {!list.isPending && rows.length === 0 && <EmptyState title={t("defects.none")} />}
      <div ref={scrollRef} className="h-[min(65vh,44rem)] overflow-y-auto rounded-md border border-border-subtle" hidden={rows.length === 0}>
        <div className="relative" style={{ height: virtualizer.getTotalSize() }}>
          {virtualizer.getVirtualItems().map((item) => {
            const row = rows[item.index];
            if (!row) return null;
            if (item.index === rows.length - 3 && list.hasNextPage && !list.isFetchingNextPage) void list.fetchNextPage();
            return (
              <label
                key={row.media_object_id}
                className="absolute inset-x-0 flex items-center gap-3 border-b border-border-subtle px-3 text-sm"
                style={{ height: item.size, transform: `translateY(${item.start}px)` }}
              >
                <input type="checkbox" checked={chosen.has(row.media_object_id)} onChange={() => toggle(row.media_object_id)} aria-label={row.path} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium">{row.work_id ? <Link to="/works/$id" params={{ id: String(row.work_id) }} className="hover:underline">{row.work_title}</Link> : row.path}</span>
                  <span className="block truncate font-mono text-[0.6875rem] text-ink-muted" title={row.detail || row.path}>{row.path}{row.detail ? ` - ${row.detail}` : ""}</span>
                </span>
                <Chip tone={row.reason === "decode_error" ? "caution" : "danger"}>{t(`defects.reason.${row.reason}` as MessageKey)}</Chip>
                <span className="w-20 text-right text-xs tabular-nums text-ink-muted">{formatSize(row.size_bytes)}</span>
              </label>
            );
          })}
        </div>
      </div>
      {confirming && (
        <ConfirmDialog
          title={t(confirming === "delete" ? "defects.confirmDelete" : "defects.confirmRerequest", { count: chosen.size })}
          consequence={t(confirming === "delete" ? "defects.deleteConsequence" : "defects.rerequestConsequence")}
          confirmLabel={t(confirming === "delete" ? "defects.delete" : "defects.rerequest", { count: chosen.size })}
          danger={confirming === "delete"}
          busy={resolve.isPending}
          onConfirm={() => resolve.mutate(confirming)}
          onCancel={() => setConfirming(undefined)}
        />
      )}
    </div>
  );
}
