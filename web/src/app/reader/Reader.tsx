import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { X } from "lucide-react";

import { t } from "@/lib/i18n";
import { Button } from "@/ui/Button";

import { ComicReader } from "./ComicReader";
import { EpubReader } from "./EpubReader";
import { PdfReader } from "./PdfReader";
import { readerKindOf } from "./readerModel";

/** The reader page: /read/<file id>?ext=cbz&title=...&rtl=1 opens the file in the reader its kind needs. */
export function Reader() {
  const { mediaObjectId } = useParams({ strict: false }) as { mediaObjectId: string };
  const search = useSearch({ strict: false }) as { ext?: string; title?: string; rtl?: string };
  const navigate = useNavigate();
  const id = Number(mediaObjectId);
  const kind = readerKindOf(search.ext);
  const close = () => (window.history.length > 1 ? window.history.back() : void navigate({ to: "/" }));
  return (
    <div className="flex h-[calc(100vh-4rem)] flex-col">
      <div className="flex items-center gap-2 px-3 py-1.5">
        <h1 className="min-w-0 flex-1 truncate text-sm font-medium text-ink">{search.title ?? t("reader.title")}</h1>
        <Button variant="icon" title={t("reader.close")} aria-label={t("reader.close")} onClick={close}>
          <X aria-hidden className="size-4" />
        </Button>
      </div>
      <div className="min-h-0 flex-1">
        {kind === "comic" && <ComicReader mediaObjectId={id} rightToLeftDefault={search.rtl === "1"} />}
        {kind === "epub" && <EpubReader mediaObjectId={id} />}
        {kind === "pdf" && <PdfReader mediaObjectId={id} />}
        {kind === null && <p className="p-6 text-sm text-danger">{t("reader.unsupported")}</p>}
      </div>
    </div>
  );
}
