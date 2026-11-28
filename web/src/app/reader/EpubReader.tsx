import { ChevronLeft, ChevronRight } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { t } from "@/lib/i18n";
import { Button } from "@/ui/Button";

import { fileUrl } from "./readerModel";
import { useReadingPosition } from "./useReadingPosition";

type Rendition = import("epubjs").Rendition;

/** epub.js (BSD-2-Clause, loaded only here) paginates the book; the place is the EPUB's own CFI. */
export function EpubReader({ mediaObjectId }: { mediaObjectId: number }) {
  const holder = useRef<HTMLDivElement>(null);
  const rendition = useRef<Rendition | null>(null);
  const { saved, save } = useReadingPosition(mediaObjectId);
  const [failed, setFailed] = useState(false);
  const [ready, setReady] = useState(false);
  const started = useRef(false);

  useEffect(() => {
    if (saved === undefined || started.current || !holder.current) return;
    started.current = true;
    const target = holder.current;
    let cancelled = false;
    let destroy: (() => void) | undefined;
    void (async () => {
      try {
        const { default: ePub } = await import("epubjs");
        const book = ePub(fileUrl(mediaObjectId), { openAs: "epub" });
        const shown = book.renderTo(target, { width: "100%", height: "100%", flow: "paginated", spread: "auto" });
        rendition.current = shown;
        destroy = () => book.destroy();
        shown.on("relocated", (location: { start: { cfi: string } }) => {
          const progress = book.locations.length() > 0 ? book.locations.percentageFromCfi(location.start.cfi) : 0;
          save(location.start.cfi, progress);
        });
        await shown.display(saved?.position || undefined);
        if (cancelled) return;
        setReady(true);
        // Locations give the progress; they take a moment to compute and the reader works without them.
        void book.locations.generate(1600).catch(() => undefined);
      } catch {
        if (!cancelled) setFailed(true);
      }
    })();
    return () => {
      cancelled = true;
      destroy?.();
    };
  }, [saved, mediaObjectId, save]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "ArrowRight" || event.key === "PageDown") void rendition.current?.next();
      else if (event.key === "ArrowLeft" || event.key === "PageUp") void rendition.current?.prev();
      else return;
      event.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  if (failed) return <p className="p-6 text-sm text-danger">{t("reader.failed")}</p>;
  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b border-border-subtle px-3 py-1.5 text-sm">
        <Button variant="icon" title={t("reader.previous")} aria-label={t("reader.previous")} onClick={() => void rendition.current?.prev()}>
          <ChevronLeft aria-hidden className="size-4" />
        </Button>
        <Button variant="icon" title={t("reader.next")} aria-label={t("reader.next")} onClick={() => void rendition.current?.next()}>
          <ChevronRight aria-hidden className="size-4" />
        </Button>
        {!ready && <span className="text-ink-muted">{t("reader.loading")}</span>}
      </div>
      <div ref={holder} className="min-h-0 flex-1 bg-white" />
    </div>
  );
}
