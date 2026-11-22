import { useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useVirtualizer } from "@tanstack/react-virtual";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { invalidateWorkLists } from "@/lib/resources";
import { t } from "@/lib/i18n";
import { Button } from "@/ui/Button";
import { ConfirmDialog } from "@/ui/ConfirmDialog";

import { formatBytes } from "../Queue";

type Row = components["schemas"]["WatchedWork"];

const ROW_HEIGHT = 40;
const DEFAULT_DAYS = 30;

/** The cleanup list: works every counted user has played, a while ago, that still have files. Recycling goes through
 *  the recycle bin and is previewed first; nothing else here removes anything. */
export function WatchedPage() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const { days: searched } = useSearch({ strict: false }) as { days?: number | string };
  const days = Number.isFinite(Number(searched)) && searched !== undefined && searched !== "" ? Math.max(0, Math.floor(Number(searched))) : DEFAULT_DAYS;
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [chosen, setChosen] = useState<ReadonlySet<number>>(new Set());
  const [unmonitor, setUnmonitor] = useState(false);
  const [asking, setAsking] = useState(false);
  const [done, setDone] = useState<string | null>(null);

  const pages = useInfiniteQuery({
    queryKey: ["watched", categoryId, days],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam, signal }) =>
      ok(api.GET("/libraries/{id}/watched", { params: { path: { id: Number(categoryId) }, query: { days, cursor: pageParam, limit: 100 } }, signal })),
    getNextPageParam: (last) => last.next_cursor,
  });
  const rows: Row[] = useMemo(() => pages.data?.pages.flatMap((page) => page.items) ?? [], [pages.data]);
  const counted = pages.data?.pages[0]?.counted_users ?? 0;

  const scroller = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => ROW_HEIGHT,
    getItemKey: (index) => rows[index]?.work_id ?? index,
    overscan: 10,
  });
  const items = virtualizer.getVirtualItems();
  const last = items[items.length - 1];
  if (last && last.index >= rows.length - 5 && pages.hasNextPage && !pages.isFetchingNextPage && !pages.isFetchNextPageError) void pages.fetchNextPage();

  const ids = [...chosen];
  const preview = useQuery({
    queryKey: ["watched-preview", categoryId, days, ids.sort((a, b) => a - b).join(",")],
    enabled: asking && ids.length > 0,
    queryFn: () => ok(api.POST("/libraries/{id}/watched/recycle", { params: { path: { id: Number(categoryId) } }, body: { work_ids: ids, days, unmonitor: false, preview: true } })),
  });
  const recycle = useMutation({
    mutationFn: () => ok(api.POST("/libraries/{id}/watched/recycle", { params: { path: { id: Number(categoryId) } }, body: { work_ids: ids, days, unmonitor, preview: false } })),
    onSuccess: (result) => {
      setDone(t("watched.done", { files: result.files }));
      setAsking(false);
      setChosen(new Set());
      void queryClient.invalidateQueries({ queryKey: ["watched", categoryId] });
      invalidateWorkLists(queryClient);
    },
  });

  const toggle = (id: number) => {
    const next = new Set(chosen);
    if (!next.delete(id)) next.add(id);
    setChosen(next);
  };
  const allChosen = rows.length > 0 && chosen.size === rows.length;

  return (
    <div className="flex h-full flex-col gap-3 p-4">
      <h1 className="text-sm font-medium">{t("watched.title")}</h1>
      <p className="text-xs text-ink-muted">{t("watched.intro")}</p>
      <div className="flex flex-wrap items-center gap-3 text-sm">
        <label className="flex items-center gap-2">
          {t("watched.days")}
          <input
            type="number"
            min={0}
            max={3650}
            value={days}
            onChange={(event) => void navigate({ to: ".", search: (prev: Record<string, unknown>) => ({ ...prev, days: event.target.value === "" ? undefined : Number(event.target.value) }) })}
            className="w-20 rounded-md border border-border-subtle bg-surface px-2 py-1"
          />
        </label>
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={allChosen} onChange={() => setChosen(allChosen ? new Set() : new Set(rows.map((row) => row.work_id)))} />
          {t("watched.selectAll")}
        </label>
        <Button variant="secondary" disabled={chosen.size === 0} onClick={() => { setUnmonitor(false); setAsking(true); }}>
          {t("watched.recycle")}
        </Button>
        <Button variant="secondary" disabled={chosen.size === 0} onClick={() => { setUnmonitor(true); setAsking(true); }}>
          {t("watched.recycleUnmonitor")}
        </Button>
        {done && <span role="status" className="text-ink-muted">{done}</span>}
      </div>
      {pages.error !== null && !pages.isLoading && <p className="text-sm text-danger">{t("watched.failed")}</p>}
      {!pages.isLoading && pages.error === null && rows.length === 0 && <p className="text-sm text-ink-muted">{t("watched.empty")}</p>}
      <div className="grid grid-cols-[2rem_1fr_6rem_9rem_7rem] gap-2 border-b border-border-subtle px-2 pb-1 text-xs text-ink-muted">
        <span />
        <span>{t("watched.columnTitle")}</span>
        <span>{t("watched.columnBy")}</span>
        <span>{t("watched.columnLast")}</span>
        <span>{t("watched.columnSize")}</span>
      </div>
      <div ref={scroller} className="min-h-0 flex-1 overflow-auto">
        <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
          {items.map((virtual) => {
            const row = rows[virtual.index];
            if (!row) return null;
            return (
              <div
                key={row.work_id}
                className="absolute inset-x-0 grid grid-cols-[2rem_1fr_6rem_9rem_7rem] items-center gap-2 border-b border-border-subtle px-2 text-sm"
                style={{ height: virtual.size, transform: `translateY(${virtual.start}px)` }}
              >
                <input type="checkbox" aria-label={row.title} checked={chosen.has(row.work_id)} onChange={() => toggle(row.work_id)} />
                <Link to="/library/$categoryId/works/$workId" params={{ categoryId, workId: String(row.work_id) }} className="truncate hover:underline">
                  {row.title}
                  {row.year ? ` (${row.year})` : ""}
                </Link>
                <span className="text-ink-muted">{`${row.watched_by}/${counted}`}</span>
                <span className="text-ink-muted">{row.last_played_at ? new Date(row.last_played_at * 1000).toLocaleDateString() : ""}</span>
                <span className="text-ink-muted">{formatBytes(row.size_bytes)}</span>
              </div>
            );
          })}
        </div>
      </div>
      {asking && (
        <ConfirmDialog
          title={t("watched.confirmTitle")}
          consequence={
            preview.data
              ? `${t("watched.consequence", { works: preview.data.works, files: preview.data.files, size: formatBytes(preview.data.bytes) })} ${unmonitor ? t("watched.consequenceUnmonitored") : t("watched.consequenceMonitored")}`
              : "…"
          }
          confirmLabel={unmonitor ? t("watched.recycleUnmonitor") : t("watched.recycle")}
          danger
          busy={recycle.isPending}
          disabled={!preview.data}
          onCancel={() => setAsking(false)}
          onConfirm={() => recycle.mutate()}
        />
      )}
    </div>
  );
}
