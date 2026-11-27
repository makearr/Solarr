import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { ListTable, type Column } from "@/list/ListTable";
import { useLibraryList } from "@/list/useLibraryList";
import { useRouteView } from "@/list/useRouteView";
import { t, type MessageKey } from "@/lib/i18n";
import { lastErrorText, reasonText } from "@/lib/transcodeReason";
import { problemText } from "@/lib/useNotifications";
import { Button } from "@/ui/Button";
import { Dialog } from "@/ui/Dialog";
import { inputStyle, labelStyle } from "@/ui/form";

import { formatSize } from "./work/workFormat";
import { useConfirm } from "@/ui/useConfirm";

type Item = components["schemas"]["TranscodeItem"];
type StreamPlan = { index: number; type: string; action: string; reason?: string; codec?: string; language?: string };

const FILTER_KEYS: readonly string[] = ["state", "library", "q"];
const STATES = [
  ["needed", "transcode.needed"],
  ["running", "transcode.running"],
  ["done", "transcode.done"],
  ["not_needed", "transcode.notNeeded"],
  ["failed", "transcode.failed"],
  ["blocked", "transcode.blocked"],
] as const satisfies readonly (readonly [string, MessageKey])[];

const quietButton = "rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted hover:text-ink";

/** Settings > Transcode > Files: every judged file, what is to happen to it and why, with retry and restore. */
export function TranscodeFiles() {
  const { ask, dialog } = useConfirm();
  const queryClient = useQueryClient();
  const libraries = useQuery({ queryKey: ["transcode-summary"], queryFn: async () => (await api.GET("/transcode/summary")).data });
  const [detail, setDetail] = useState<Item | null>(null);
  const [retrying, setRetrying] = useState(false);
  const [notice, setNotice] = useState("");
  const [failure, setFailure] = useState("");
  const list = useLibraryList<Item>(
    {
      name: "transcode-files",
      idOf: (item) => item.media_object_id,
      topics: ["library"],
      accepts: (event) => event.entity.startsWith("transcode:"),
      fetchPage: async ({ cursor, query, signal }) => {
        const { data, error } = await api.GET("/transcode/items", {
          params: {
            query: {
              cursor,
              limit: 100,
              state: (query.filters.state || undefined) as Item["state"] | undefined,
              category_id: query.filters.library ? Number(query.filters.library) : undefined,
              q: query.filters.q || undefined,
            },
          },
          signal,
        });
        if (error) throw error;
        return { items: data.items, next_cursor: data.next_cursor };
      },
    },
    useRouteView(FILTER_KEYS),
  );
  const retryPlain = useMutation({
    mutationFn: async () => {
      const selection = list.selectionRequest;
      if (!selection) return;
      const { error } = await api.POST("/transcode/items/retry", { body: retryRequest(selection, {}, false) });
      if (error) throw error;
    },
    onSuccess: () => {
      list.clearSelection();
      void queryClient.invalidateQueries({ queryKey: ["transcode-summary"] });
      void queryClient.invalidateQueries({ queryKey: ["transcode-files"] });
    },
  });
  const restore = useMutation({
    mutationFn: async (item: Item) => {
      const { error } = await api.POST("/transcode/items/{media_object_id}/restore", { params: { path: { media_object_id: item.media_object_id } } });
      if (error) throw error;
    },
    onError: (error) => setFailure(problemText(error)),
    onSuccess: () => {
      setFailure("");
      setNotice(t("transcode.files.restored"));
      void queryClient.invalidateQueries({ queryKey: ["transcode-summary"] });
      void queryClient.invalidateQueries({ queryKey: ["transcode-files"] });
    },
  });
  const columns: readonly Column<Item>[] = [
    { key: "path", header: t("transcode.files.file"), render: (item) => <span className="block truncate" title={item.path}>{item.path}</span> },
    { key: "state", header: t("transcode.files.state"), width: "7rem", render: (item) => <StateBadge item={item} /> },
    { key: "why", header: t("transcode.files.why"), render: (item) => <span className="block truncate" title={item.reasons.map(reasonText).join("; ")}>{item.reasons.map(reasonText).join("; ")}</span> },
    {
      key: "size",
      header: t("transcode.files.size"),
      width: "11rem",
      render: (item) =>
        item.state === "done" ? `${formatSize(item.original_size_bytes ?? 0)} → ${formatSize(item.result_size_bytes ?? 0)}` : formatSize(item.original_size_bytes ?? 0),
    },
    { key: "attempts", header: t("transcode.files.attempts"), width: "4rem", render: (item) => (item.attempts > 0 ? item.attempts : item.pass > 1 ? item.pass : "") },
    { key: "error", header: t("transcode.files.error"), render: (item) => <span className="block truncate text-danger" title={lastErrorText(item.last_error || item.failure_code || "")}>{lastErrorText(item.last_error || item.failure_code || "")}</span> },
    {
      key: "actions",
      header: "",
      width: "12rem",
      render: (item) => (
        <span className="flex gap-2" onClick={(event) => event.stopPropagation()}>
          <button type="button" className={quietButton} onClick={() => setDetail(item)}>
            {t("transcode.files.details")}
          </button>
          {item.restorable && (
            <button
              type="button"
              className={quietButton}
              disabled={restore.isPending}
              onClick={() =>
                ask({
                  title: t("transcode.files.restore"),
                  consequence: t("transcode.files.restoreConfirm", { date: item.restorable_until ? new Date(item.restorable_until).toLocaleDateString() : "-" }),
                  confirmLabel: t("transcode.files.restore"),
                  danger: false,
                  run: () => restore.mutate(item),
                })
              }
            >
              {t("transcode.files.restore")}
            </button>
          )}
        </span>
      ),
    },
  ];
  const request = list.selectionRequest;
  return (
    <div className="flex h-full flex-col">
      {dialog}
      <h1 className="sr-only">{t("transcode.filesLink")}</h1>
      <div className="flex flex-wrap items-center gap-2 border-b border-border-subtle p-3">
        <Link to="/settings/transcode" className="text-xs text-ink-muted hover:text-ink">
          {t("transcode.setup.back")}
        </Link>
        <select aria-label={t("transcode.files.state")} className={`${inputStyle} text-xs`} value={list.query.filters.state ?? ""} onChange={(event) => list.filterBy("state", event.target.value)}>
          <option value="">{t("transcode.files.anyState")}</option>
          {STATES.map(([state, label]) => (
            <option key={state} value={state}>
              {t(label)}
            </option>
          ))}
        </select>
        <select aria-label={t("transcode.libraries")} className={`${inputStyle} text-xs`} value={list.query.filters.library ?? ""} onChange={(event) => list.filterBy("library", event.target.value)}>
          <option value="">{t("transcode.files.anyLibrary")}</option>
          {libraries.data?.libraries.map((library) => (
            <option key={library.category_id} value={library.category_id}>
              {library.name}
            </option>
          ))}
        </select>
        <input
          type="search"
          aria-label={t("transcode.files.search")}
          placeholder={t("transcode.files.search")}
          className={`${inputStyle} text-xs`}
          defaultValue={list.query.filters.q ?? ""}
          onChange={(event) => list.filterBy("q", event.target.value)}
        />
        <div className="ml-auto flex flex-wrap items-center gap-2 text-xs">
          {!list.selectionIsEmpty && (
            <>
              <span className="text-ink-muted">{list.selectionCount === null ? t("transcode.files.allMatching") : t("transcode.files.selected", { count: list.selectionCount })}</span>
              <button type="button" className={quietButton} onClick={list.clearSelection}>
                {t("transcode.files.clear")}
              </button>
              <button type="button" className={quietButton} disabled={retryPlain.isPending} onClick={() => retryPlain.mutate()}>
                {t("transcode.files.retry")}
              </button>
              <button type="button" className={quietButton} onClick={() => setRetrying(true)}>
                {t("transcode.files.retryWith")}
              </button>
            </>
          )}
          <button type="button" className={quietButton} onClick={list.selectAllMatching}>
            {t("transcode.files.selectAll")}
          </button>
        </div>
      </div>
      {failure && <p role="alert" className="px-3 py-1 text-xs text-danger">{failure}</p>}
      {notice && <p role="status" className="px-3 py-1 text-xs text-ok">{notice}</p>}
      <div className="min-h-0 flex-1">
        {list.isLoading && <p className="p-6 text-sm text-ink-muted">{t("transcode.files.loading")}</p>}
        {list.error !== null && <p className="p-6 text-sm text-ink-muted">{t("transcode.files.readFailed")}</p>}
        {!list.isLoading && list.error === null && list.items.length === 0 && <p className="p-6 text-sm text-ink-muted">{t("transcode.files.empty")}</p>}
        {list.items.length > 0 && <ListTable list={list} columns={columns} idOf={(item) => item.media_object_id} />}
      </div>
      {detail && <DetailDialog id={detail.media_object_id} onClose={() => setDetail(null)} />}
      {retrying && request && (
        <RetryDialog
          selection={request}
          onClose={() => setRetrying(false)}
          onDone={() => {
            setRetrying(false);
            list.clearSelection();
            void queryClient.invalidateQueries({ queryKey: ["transcode-summary"] });
            void queryClient.invalidateQueries({ queryKey: ["transcode-files"] });
          }}
        />
      )}
    </div>
  );
}

function StateBadge({ item }: { item: Item }) {
  const tone = item.state === "failed" || item.state === "blocked" ? "text-danger" : item.state === "done" ? "text-ok" : item.state === "running" ? "text-accent" : "text-ink-muted";
  const label = STATES.find(([state]) => state === item.state)?.[1];
  return <span className={tone}>{label ? t(label) : item.state}</span>;
}

function DetailDialog({ id, onClose }: { id: number; onClose: () => void }) {
  const item = useQuery({ queryKey: ["transcode-item", id], queryFn: async () => (await api.GET("/transcode/items/{media_object_id}", { params: { path: { media_object_id: id } } })).data });
  const plan = item.data?.plan as { video?: StreamPlan[]; audio?: StreamPlan[]; subtitles?: StreamPlan[]; dropped?: StreamPlan[]; container?: string } | undefined;
  const streams = plan ? [...(plan.video ?? []), ...(plan.audio ?? []), ...(plan.subtitles ?? []), ...(plan.dropped ?? [])] : [];
  return (
    <Dialog title={item.data?.path ?? ""} width="wide" onClose={onClose} closeLabel={t("transcode.retryWith.close")}>
      <div className="flex flex-col gap-3 text-sm">
        <section>
          <h3 className="text-xs font-medium text-ink-muted">{t("transcode.plan.reasons")}</h3>
          <ul className="list-disc pl-5">
            {item.data?.reasons.map((reason, index) => (
              <li key={index}>{reasonText(reason)}</li>
            ))}
            {item.data && (item.data.last_error || item.data.failure_code) && <li className="text-danger">{lastErrorText(item.data.last_error || item.data.failure_code || "")}</li>}
          </ul>
        </section>
        <section>
          <h3 className="text-xs font-medium text-ink-muted">{t("transcode.plan")}</h3>
          {streams.length === 0 ? (
            <p className="text-xs text-ink-muted">{t("transcode.plan.none")}</p>
          ) : (
            <table className="w-full text-xs">
              <tbody>
                {streams.map((stream) => (
                  <tr key={`${stream.type}-${stream.index}`} className="border-t border-border-subtle">
                    <td className="py-1 pr-3">
                      {stream.type} #{stream.index}
                    </td>
                    <td className="pr-3">{stream.codec ?? ""}</td>
                    <td className="pr-3">{stream.language ?? ""}</td>
                    <td className="pr-3">{stream.action === "copy" ? t("transcode.plan.copy") : stream.action === "drop" ? t("transcode.plan.drop") : t("transcode.plan.encode")}</td>
                    <td className="text-ink-muted">{stream.reason ? reasonText({ code: stream.reason, stream: stream.index }) : ""}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>
      </div>
    </Dialog>
  );
}

type Selection = NonNullable<ReturnType<typeof useLibraryList<Item>>["selectionRequest"]>;

/** The retry request of a selection: the chosen ids, or the filter with the files left out. */
function retryRequest(selection: Selection, override: components["schemas"]["TranscodeRetryRequest"]["override"], preview: boolean): components["schemas"]["TranscodeRetryRequest"] {
  return {
    ...(selection.kind === "ids"
      ? { ids: [...selection.ids] }
      : {
          state: selection.filters.state === "failed" || selection.filters.state === "blocked" ? selection.filters.state : undefined,
          category_id: selection.filters.library ? Number(selection.filters.library) : undefined,
          q: selection.filters.q || undefined,
          excluded_ids: selection.excluded,
        }),
    override,
    preview,
  };
}

function RetryDialog({ selection, onClose, onDone }: { selection: Selection; onClose: () => void; onDone: () => void }) {
  const [quality, setQuality] = useState("");
  const [bitrate, setBitrate] = useState("");
  const [preset, setPreset] = useState("");
  const [engine, setEngine] = useState("");
  const body = (preview: boolean) =>
    retryRequest(selection, {
      quality: quality ? Number(quality) : undefined,
      video_bitrate_kbps: bitrate ? Number(bitrate) : undefined,
      preset: (preset || undefined) as "fast" | undefined,
      engine: (engine || undefined) as "auto" | undefined,
    }, preview);
  const count = useQuery({
    queryKey: ["transcode-retry-preview", selection, quality, bitrate, preset, engine],
    queryFn: async () => (await api.POST("/transcode/items/retry", { body: body(true) })).data,
  });
  const go = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/transcode/items/retry", { body: body(false) });
      if (error) throw error;
    },
    onSuccess: onDone,
  });
  return (
    <Dialog
      title={t("transcode.retryWith.title")}
      description={t("transcode.retryWith.help")}
      onClose={onClose}
      closeLabel={t("transcode.retryWith.close")}
      actions={
        <Button variant="primary" busy={go.isPending} disabled={!count.data || count.data.count === 0} onClick={() => go.mutate()}>
          {t("transcode.retryWith.go")}
        </Button>
      }
    >
      <div className="flex flex-col gap-3">
        <label className={labelStyle}>
          {t("transcode.retryWith.quality")}
          <input type="number" min={0} className={inputStyle} value={quality} onChange={(event) => setQuality(event.target.value)} />
        </label>
        <label className={labelStyle}>
          {t("transcode.retryWith.bitrate")}
          <input type="number" min={0} className={inputStyle} value={bitrate} onChange={(event) => setBitrate(event.target.value)} />
        </label>
        <label className={labelStyle}>
          {t("transcode.retryWith.preset")}
          <select className={inputStyle} value={preset} onChange={(event) => setPreset(event.target.value)}>
            <option value="" />
            {["fast", "normal", "slow", "slower", "veryslow"].map((one) => (
              <option key={one} value={one}>
                {one}
              </option>
            ))}
          </select>
        </label>
        <label className={labelStyle}>
          {t("transcode.retryWith.engine")}
          <select className={inputStyle} value={engine} onChange={(event) => setEngine(event.target.value)}>
            <option value="" />
            {["auto", "software", "qsv", "vaapi", "nvenc"].map((one) => (
              <option key={one} value={one}>
                {one}
              </option>
            ))}
          </select>
        </label>
        <p role="status" className="text-sm">
          {count.data ? t("transcode.retryWith.count", { count: count.data.count }) : ""}
        </p>
      </div>
    </Dialog>
  );
}
