import { Link } from "@tanstack/react-router";
import { BookOpen, Play } from "lucide-react";

import { t } from "@/lib/i18n";
import type { components } from "@/api/schema";
import { Button } from "@/ui/Button";
import { useLibrary } from "@/lib/useLibrary";

import { usePlayer } from "../player/PlayerContext";
import { isPlayableExtension, type PlayerItem } from "../player/playerModel";
import { readerKindOf } from "../reader/readerModel";
import { useAllParts } from "./WorkParts";

type Work = components["schemas"]["WorkDetail"];
type PartFile = components["schemas"]["PartFile"];

/** The one prominent way into a work's own files, beside its cover: Play for audio and video, Read for comics, EPUB and PDF. */
export function WorkStart({ work, categoryId }: { work: Work; categoryId: string | undefined }) {
  const player = usePlayer();
  const kind = useLibrary(categoryId)?.kind;
  const { parts } = useAllParts(work.id, undefined, work.part_count > 0);
  const entries: { file: PartFile; title: string; durationMs?: number }[] = [
    ...(work.files ?? []).map((file) => ({ file, title: work.title, durationMs: work.runtime_ms ?? undefined })),
    ...parts.flatMap((part) => (part.file ? [{ file: part.file, title: part.title || work.title, durationMs: part.runtime_ms ?? undefined }] : [])),
  ];
  const playable = entries.filter((entry) => isPlayableExtension(entry.file.extension));
  const readable = entries.find((entry) => readerKindOf(entry.file.extension) !== null);
  if (playable.length > 0) {
    const queue: PlayerItem[] = playable.map((entry) => ({ mediaObjectId: entry.file.media_object_id, title: entry.title, durationMs: entry.durationMs, file: entry.file }));
    return (
      <Button variant="primary" className="w-full" icon={<Play aria-hidden className="size-4" />} onClick={() => player.play(queue)}>
        {t("player.play")}
      </Button>
    );
  }
  if (!readable) return null;
  // Manga and hentai libraries read right to left.
  const rightToLeft = kind === "manga" || kind === "hentai";
  return (
    <Link
      to="/read/$mediaObjectId"
      params={{ mediaObjectId: String(readable.file.media_object_id) }}
      search={{ ext: (readable.file.extension ?? "").replace(/^\./, "").toLowerCase(), title: readable.title, ...(rightToLeft ? { rtl: "1" } : {}) }}
      className="inline-flex w-full items-center justify-center gap-1.5 rounded-md border border-accent bg-accent px-3 py-1.5 text-sm text-accent-ink hover:opacity-90 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
    >
      <BookOpen aria-hidden className="size-4" />
      {t("reader.read")}
    </Link>
  );
}
