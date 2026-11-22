import { useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";
import { Button } from "@/ui/Button";
import { Chip } from "@/ui/Chip";
import { Dialog } from "@/ui/Dialog";
import { inputStyle } from "@/ui/form";

type Preview = components["schemas"]["ReadingListPreview"];
type Book = Preview["books"][number];

const TONE = { matched: "ok", ambiguous: "caution", not_found: "warn" } as const;

/** Imports a ComicRack reading list: shows what matched in the library, then keeps the matched issues in order as one list. */
export function ReadingListImport({ categoryId, onClose }: { categoryId: string; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [preview, setPreview] = useState<Preview>();
  const [name, setName] = useState("");
  const [problem, setProblem] = useState("");
  const scroller = useRef<HTMLDivElement>(null);
  const read = useMutation({
    meta: { handlesErrors: true },
    mutationFn: async (file: File) => {
      const { data, error } = await api.POST("/categories/{id}/reading-list", { params: { path: { id: Number(categoryId) } }, body: { content: await file.text() } });
      if (error) throw new Error(error.detail ?? t("readingList.failed"));
      return data;
    },
    onSuccess: (data) => {
      setPreview(data);
      setName(data.name);
      setProblem("");
    },
    onError: (error) => setProblem(error.message),
  });
  const matched = (preview?.books ?? []).filter((book) => book.part_id !== undefined);
  const create = useMutation({
    meta: { handlesErrors: true },
    mutationFn: () => ok(api.POST("/crossovers", { body: { name, category_id: Number(categoryId), part_ids: matched.map((book) => book.part_id!) } })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["crossovers", categoryId] });
      onClose();
    },
    onError: (error) => setProblem(error.message),
  });
  const books = preview?.books ?? [];
  const rows = useVirtualizer({ count: books.length, getScrollElement: () => scroller.current, estimateSize: () => 36, overscan: 8 });
  return (
    <Dialog
      title={t("readingList.title")}
      description={t("readingList.help")}
      onClose={onClose}
      actions={
        <>
          <Button onClick={onClose}>{t("action.cancel")}</Button>
          <Button variant="primary" busy={create.isPending} disabled={matched.length === 0 || name.trim() === ""} onClick={() => create.mutate()}>
            {t("readingList.create", { count: matched.length })}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        <input type="file" accept=".cbl,text/xml,application/xml" onChange={(event) => event.target.files?.[0] && read.mutate(event.target.files[0])} className="text-sm" />
        {problem && <p className="text-sm text-danger">{problem}</p>}
        {preview && (
          <>
            <input value={name} onChange={(event) => setName(event.target.value)} aria-label={t("readingList.name")} className={inputStyle} />
            <p className="text-xs text-ink-muted">{t("readingList.counts", { matched: matched.length, total: books.length })}</p>
            <div ref={scroller} className="h-72 overflow-y-auto rounded-lg border border-border-subtle">
              <div style={{ height: rows.getTotalSize(), position: "relative" }}>
                {rows.getVirtualItems().map((row) => (
                  <BookRow key={row.index} book={books[row.index]!} top={row.start} />
                ))}
              </div>
            </div>
          </>
        )}
      </div>
    </Dialog>
  );
}

function BookRow({ book, top }: { book: Book; top: number }) {
  return (
    <div style={{ position: "absolute", top, left: 0, right: 0, height: 36 }} className="flex items-center gap-3 border-b border-border-subtle px-3 text-sm">
      <span className="min-w-0 flex-1 truncate">{book.series_title ?? book.series} #{book.number}</span>
      <Chip tone={TONE[book.status]} title={book.missing ? t(`readingList.missing.${book.missing}`) : undefined}>
        {t(`readingList.status.${book.status}`)}
      </Chip>
    </div>
  );
}
