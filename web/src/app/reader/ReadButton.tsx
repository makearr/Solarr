import { Link } from "@tanstack/react-router";
import { BookOpen } from "lucide-react";

import { t } from "@/lib/i18n";

import { readerKindOf } from "./readerModel";

/** Opens a comic, EPUB or PDF in the built-in reader; nothing for a file the reader cannot show. */
export function ReadButton({ mediaObjectId, extension, title, rightToLeft = false, className }: { mediaObjectId: number; extension: string | undefined; title: string; rightToLeft?: boolean; className?: string }) {
  if (readerKindOf(extension) === null) return null;
  const label = `${t("reader.read")} ${title}`;
  return (
    <Link
      to="/read/$mediaObjectId"
      params={{ mediaObjectId: String(mediaObjectId) }}
      search={{ ext: (extension ?? "").replace(/^\./, "").toLowerCase(), title, ...(rightToLeft ? { rtl: "1" } : {}) }}
      title={label}
      aria-label={label}
      className={`inline-flex items-center justify-center rounded-md p-1 text-ink-muted hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent ${className ?? ""}`}
    >
      <BookOpen aria-hidden className="size-3.5" />
    </Link>
  );
}
