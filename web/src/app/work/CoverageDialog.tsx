import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";

import { api, ok } from "@/api/client";
import { t } from "@/lib/i18n";
import { Button } from "@/ui/Button";
import { Dialog } from "@/ui/Dialog";
import { inputStyle } from "@/ui/form";

interface Covered {
  work_id: number;
  title: string;
  media_ref: string;
}

/** Which issues a held edition stands for: a series and a range of its parts, kept as hand-made rows. */
export function CoverageDialog({ workId, title, onClose }: { workId: number; title: string; onClose: () => void }) {
  const queryClient = useQueryClient();
  const stored = useQuery({ queryKey: ["work-coverage", workId], queryFn: () => ok(api.GET("/works/{id}/coverage", { params: { path: { id: workId } } })) });
  const [editionRef, setEditionRef] = useState<string>();
  const [edited, setEdited] = useState<Covered[]>();
  const [query, setQuery] = useState("");
  const [seriesId, setSeriesId] = useState<number>();
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");

  const ref = editionRef ?? stored.data?.edition_parts[0] ?? "";
  const items = stored.data?.items ?? [];
  const byHand = items.filter((item) => item.source === "hand" && item.edition_ref === ref);
  const rows: Covered[] = edited ?? byHand.map((item) => ({ work_id: item.work_id, title: item.title, media_ref: item.media_ref }));
  const byProvider = items.filter((item) => item.source !== "hand" && item.edition_ref === ref);

  const series = useQuery({
    queryKey: ["coverage-series", workId, query],
    enabled: query.trim().length >= 2,
    queryFn: () => {
      const where = JSON.stringify([{ field: "title", op: "contains", value: query.trim() }]);
      return ok(api.GET("/works", { params: { query: { where, limit: 20 } } }));
    },
  });
  const parts = useQuery({
    queryKey: ["coverage-parts", seriesId],
    enabled: seriesId !== undefined,
    queryFn: () => ok(api.GET("/works/{id}/parts", { params: { path: { id: seriesId! }, query: { limit: 500 } } })),
  });
  const chosen = series.data?.items.find((work) => work.id === seriesId);
  const options = parts.data?.items ?? [];

  const addRange = () => {
    const first = options.findIndex((part) => part.media_ref === from);
    const last = options.findIndex((part) => part.media_ref === (to || from));
    if (seriesId === undefined || first < 0 || last < 0) return;
    const added = options
      .slice(Math.min(first, last), Math.max(first, last) + 1)
      .map((part) => ({ work_id: seriesId, title: chosen?.title ?? "", media_ref: part.media_ref }))
      .filter((part) => !rows.some((row) => row.work_id === part.work_id && row.media_ref === part.media_ref));
    setEdited([...rows, ...added]);
  };

  const save = useMutation({
    meta: { handlesErrors: true },
    mutationFn: () =>
      ok(api.PUT("/works/{id}/coverage", { params: { path: { id: workId } }, body: { edition_ref: ref, covered: rows.map((row) => ({ work_id: row.work_id, media_ref: row.media_ref })) } })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["work-coverage"] });
      void queryClient.invalidateQueries({ queryKey: ["work"] });
      onClose();
    },
  });

  return (
    <Dialog
      title={t("coverage.title", { title })}
      description={t("coverage.help")}
      width="wide"
      onClose={onClose}
      actions={
        <>
          <Button onClick={onClose}>{t("action.cancel")}</Button>
          <Button variant="primary" busy={save.isPending} disabled={ref === ""} onClick={() => save.mutate()}>
            {t("coverage.save")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4 text-sm">
        {(stored.data?.edition_parts.length ?? 0) > 1 && (
          <select className={inputStyle} value={ref} aria-label={t("coverage.part")} onChange={(event) => { setEditionRef(event.target.value); setEdited(undefined); }}>
            {stored.data?.edition_parts.map((part) => (
              <option key={part} value={part}>{part}</option>
            ))}
          </select>
        )}
        <section className="flex flex-col gap-1">
          <h3 className="font-medium">{t("coverage.covers")}</h3>
          {rows.length === 0 && byProvider.length === 0 && <p className="text-xs text-ink-muted">{t("coverage.none")}</p>}
          <ul className="flex max-h-48 flex-col overflow-auto">
            {rows.map((row) => (
              <li key={`${row.work_id}:${row.media_ref}`} className="flex items-center gap-2 border-b border-border-subtle py-1">
                <span className="flex-1 truncate">{row.title} {row.media_ref}</span>
                <button type="button" aria-label={t("coverage.remove", { ref: row.media_ref })} className="text-ink-muted hover:text-danger" onClick={() => setEdited(rows.filter((one) => one !== row))}>
                  <Trash2 aria-hidden className="size-4" />
                </button>
              </li>
            ))}
            {byProvider.map((item) => (
              <li key={`${item.work_id}:${item.media_ref}:p`} className="flex items-center gap-2 border-b border-border-subtle py-1 text-ink-muted">
                <span className="flex-1 truncate">{item.title} {item.media_ref}</span>
                <span className="text-xs">{item.source}</span>
              </li>
            ))}
          </ul>
        </section>
        <section className="flex flex-col gap-2">
          <h3 className="font-medium">{t("coverage.add")}</h3>
          <input className={inputStyle} value={query} placeholder={t("coverage.findSeries")} aria-label={t("coverage.findSeries")} onChange={(event) => { setQuery(event.target.value); setSeriesId(undefined); setFrom(""); setTo(""); }} />
          {series.data && (
            <select className={inputStyle} value={seriesId ?? ""} aria-label={t("coverage.series")} onChange={(event) => { setSeriesId(Number(event.target.value) || undefined); setFrom(""); setTo(""); }}>
              <option value="">{t("coverage.series")}</option>
              {series.data.items.filter((work) => work.id !== workId).map((work) => (
                <option key={work.id} value={work.id}>{work.title}</option>
              ))}
            </select>
          )}
          {seriesId !== undefined && (
            <div className="flex flex-wrap items-center gap-2">
              <select className={inputStyle} value={from} aria-label={t("coverage.from")} onChange={(event) => setFrom(event.target.value)}>
                <option value="">{t("coverage.from")}</option>
                {options.map((part) => (
                  <option key={part.id} value={part.media_ref}>{part.media_ref}</option>
                ))}
              </select>
              <select className={inputStyle} value={to} aria-label={t("coverage.to")} onChange={(event) => setTo(event.target.value)}>
                <option value="">{t("coverage.to")}</option>
                {options.map((part) => (
                  <option key={part.id} value={part.media_ref}>{part.media_ref}</option>
                ))}
              </select>
              <Button disabled={from === ""} onClick={addRange}>{t("coverage.addRange")}</Button>
            </div>
          )}
        </section>
        {save.isError && <p className="text-xs text-danger">{t("coverage.failed")}</p>}
      </div>
    </Dialog>
  );
}
