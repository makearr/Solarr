import { ChevronLeft, ChevronRight } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import { t } from "@/lib/i18n";
import { Button } from "@/ui/Button";

import { fileUrl, progressOf } from "./readerModel";
import { useReadingPosition } from "./useReadingPosition";

type PdfDocument = import("pdfjs-dist").PDFDocumentProxy;

/** PDF.js (Apache-2.0, loaded only here) draws one page at a time to fit the width; the page number is the place. */
export function PdfReader({ mediaObjectId }: { mediaObjectId: number }) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const holder = useRef<HTMLDivElement>(null);
  const { saved, save } = useReadingPosition(mediaObjectId);
  const [document, setDocument] = useState<PdfDocument | null>(null);
  const [page, setPage] = useState<number | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    let opened: PdfDocument | null = null;
    void (async () => {
      try {
        const pdfjs = await import("pdfjs-dist");
        pdfjs.GlobalWorkerOptions.workerSrc = new URL("pdfjs-dist/build/pdf.worker.min.mjs", import.meta.url).toString();
        const loaded = await pdfjs.getDocument({ url: fileUrl(mediaObjectId), withCredentials: true }).promise;
        if (cancelled) return void loaded.destroy();
        opened = loaded;
        setDocument(loaded);
      } catch {
        if (!cancelled) setFailed(true);
      }
    })();
    return () => {
      cancelled = true;
      void opened?.destroy();
    };
  }, [mediaObjectId]);

  useEffect(() => {
    if (page === null && document && saved !== undefined) {
      const resumed = Number(saved?.position ?? 1);
      setPage(Number.isFinite(resumed) ? Math.min(Math.max(Math.trunc(resumed), 1), document.numPages) : 1);
    }
  }, [page, document, saved]);

  useEffect(() => {
    if (!document || page === null || !canvas.current || !holder.current) return;
    let cancelled = false;
    const target = canvas.current;
    const width = holder.current.clientWidth;
    void document.getPage(page).then((one) => {
      if (cancelled) return;
      const base = one.getViewport({ scale: 1 });
      const viewport = one.getViewport({ scale: Math.min(width / base.width, 3) * (window.devicePixelRatio || 1) });
      target.width = viewport.width;
      target.height = viewport.height;
      target.style.width = `${viewport.width / (window.devicePixelRatio || 1)}px`;
      const context = target.getContext("2d");
      if (context) void one.render({ canvasContext: context, viewport }).promise.catch(() => undefined);
    });
    return () => {
      cancelled = true;
    };
  }, [document, page]);

  const go = useCallback(
    (delta: number) => {
      if (!document || page === null) return;
      const next = Math.min(Math.max(page + delta, 1), document.numPages);
      setPage(next);
      save(String(next), progressOf(next - 1, document.numPages));
    },
    [document, page, save],
  );
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "ArrowRight" || event.key === "PageDown") go(1);
      else if (event.key === "ArrowLeft" || event.key === "PageUp") go(-1);
      else return;
      event.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [go]);

  if (failed) return <p className="p-6 text-sm text-danger">{t("reader.failed")}</p>;
  if (!document || page === null) return <p className="p-6 text-sm text-ink-muted">{t("reader.loading")}</p>;
  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b border-border-subtle px-3 py-1.5 text-sm">
        <Button variant="icon" title={t("reader.previous")} aria-label={t("reader.previous")} onClick={() => go(-1)}>
          <ChevronLeft aria-hidden className="size-4" />
        </Button>
        <span className="tabular-nums text-ink-muted">{t("reader.pageOf", { page, count: document.numPages })}</span>
        <Button variant="icon" title={t("reader.next")} aria-label={t("reader.next")} onClick={() => go(1)}>
          <ChevronRight aria-hidden className="size-4" />
        </Button>
      </div>
      <div ref={holder} className="min-h-0 flex-1 overflow-auto bg-surface">
        <canvas ref={canvas} className="mx-auto block" />
      </div>
    </div>
  );
}
