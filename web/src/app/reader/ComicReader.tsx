import { useQuery } from "@tanstack/react-query";
import { BookOpen, ChevronLeft, ChevronRight, Columns2, ArrowLeftRight } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { api, ok } from "@/api/client";
import { t } from "@/lib/i18n";
import { usePreference } from "@/lib/usePreference";
import { Button } from "@/ui/Button";

import { leftTurnsForward, pageUrl, progressOf, spreadAt, turn } from "./readerModel";
import { useReadingPosition } from "./useReadingPosition";

/** Our own page reader for cbz, cbr, cb7 and cbt: the server streams the pages in reading order; right to left for manga,
 *  two pages side by side, and the place kept per user. */
export function ComicReader({ mediaObjectId, rightToLeftDefault }: { mediaObjectId: number; rightToLeftDefault: boolean }) {
  const pages = useQuery({
    queryKey: ["file-pages", mediaObjectId],
    queryFn: () => ok(api.GET("/files/{mediaObjectId}/pages", { params: { path: { mediaObjectId } } })),
    staleTime: Infinity,
  });
  const { saved, save } = useReadingPosition(mediaObjectId);
  const [double, setDouble] = usePreference("reader.double_page", false);
  const [rightToLeft, setRightToLeft] = usePreference("reader.right_to_left", rightToLeftDefault);
  const [position, setPosition] = useState<number | null>(null);
  const count = pages.data?.count ?? 0;

  // The saved place is taken once, when it has loaded.
  useEffect(() => {
    if (position === null && saved !== undefined && pages.data) {
      const resumed = Number(saved?.position ?? 0);
      setPosition(Number.isFinite(resumed) ? Math.min(Math.max(Math.trunc(resumed), 0), Math.max(count - 1, 0)) : 0);
    }
  }, [position, saved, pages.data, count]);

  const go = useCallback(
    (forward: boolean) => {
      if (position === null) return;
      const next = turn(position, count, double, forward);
      setPosition(next);
      save(String(next), progressOf(next, count));
    },
    [position, count, double, save],
  );

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.target instanceof HTMLElement && ["INPUT", "SELECT", "TEXTAREA"].includes(event.target.tagName)) return;
      if (event.key === "ArrowRight") go(!leftTurnsForward(rightToLeft));
      else if (event.key === "ArrowLeft") go(leftTurnsForward(rightToLeft));
      else if (event.key === " " || event.key === "PageDown") go(true);
      else if (event.key === "PageUp") go(false);
      else if (event.key === "Home" && position !== null) setPosition(0);
      else if (event.key === "End" && count > 0) setPosition(count - 1);
      else return;
      event.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [go, rightToLeft, position, count]);

  // The next spread is fetched while this one is read.
  useEffect(() => {
    if (position === null || count === 0) return;
    for (const page of spreadAt(turn(position, count, double, true), count, double)) new Image().src = pageUrl(mediaObjectId, page);
  }, [position, count, double, mediaObjectId]);

  if (pages.isPending || (position === null && pages.data)) return <p className="p-6 text-sm text-ink-muted">{t("reader.loading")}</p>;
  if (pages.error || count === 0) return <p className="p-6 text-sm text-danger">{t("reader.failed")}</p>;
  const shown = spreadAt(position ?? 0, count, double);
  // Reading right to left puts the earlier page of a pair on the right.
  const ordered = rightToLeft ? [...shown].reverse() : shown;
  return (
    <div className="flex h-full flex-col">
      <div className="flex flex-wrap items-center gap-2 border-b border-border-subtle px-3 py-1.5 text-sm">
        <Button variant="icon" title={t("reader.previous")} aria-label={t("reader.previous")} onClick={() => go(false)}>
          <ChevronLeft aria-hidden className="size-4" />
        </Button>
        <span className="tabular-nums text-ink-muted">{t("reader.pageOf", { page: (shown[shown.length - 1] ?? 0) + 1, count })}</span>
        <Button variant="icon" title={t("reader.next")} aria-label={t("reader.next")} onClick={() => go(true)}>
          <ChevronRight aria-hidden className="size-4" />
        </Button>
        <Button variant="ghost" aria-pressed={double} onClick={() => setDouble(!double)} icon={<Columns2 aria-hidden className="size-4" />}>
          {t("reader.doublePage")}
        </Button>
        <Button variant="ghost" aria-pressed={rightToLeft} onClick={() => setRightToLeft(!rightToLeft)} icon={<ArrowLeftRight aria-hidden className="size-4" />}>
          {t("reader.rightToLeft")}
        </Button>
        <BookOpen aria-hidden className="ml-auto size-4 text-ink-muted" />
      </div>
      <div className="relative flex min-h-0 flex-1 items-center justify-center bg-black/90">
        {ordered.map((page) => (
          <img key={page} src={pageUrl(mediaObjectId, page)} alt={t("reader.pageAlt", { page: page + 1 })} className="max-h-full min-w-0 flex-1 object-contain" style={{ maxWidth: `${100 / ordered.length}%` }} />
        ))}
        <button type="button" aria-label={leftTurnsForward(rightToLeft) ? t("reader.next") : t("reader.previous")} onClick={() => go(leftTurnsForward(rightToLeft))} className="absolute inset-y-0 left-0 w-1/4 cursor-w-resize opacity-0" />
        <button type="button" aria-label={leftTurnsForward(rightToLeft) ? t("reader.previous") : t("reader.next")} onClick={() => go(!leftTurnsForward(rightToLeft))} className="absolute inset-y-0 right-0 w-1/4 cursor-e-resize opacity-0" />
      </div>
    </div>
  );
}
