import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";

import { api, ok } from "@/api/client";
import { vocabularyOf } from "@/lib/i18n";
import { remoteImage } from "@/lib/remoteImage";
import { useLibrary } from "@/lib/useLibrary";
import { useMediaTypes } from "@/lib/useMediaTypes";

import { useDebouncedValue } from "./useDebouncedValue";

/** What a person said the files are: a work, and for a type whose parts are wanted one by one its group and parts. */
export interface IdentityChoice {
  provider: string;
  external_id: string;
  group?: number;
  parts?: number[];
}

const NUMBERED_FOLDER = /^(cd|disc|disk|season|series|staffel)\s*\d+$|^specials$/i;

/** "Artist/Album (2011)/CD1/01.flac" -> "Artist Album", "Show (2014)/Season 01/x.mkv" -> "Show": the folders are the
 *  best first guess; a folder's own path ends in "/". */
export function queryFromPath(relPath: string): string {
  const folders = relPath
    .split("/")
    .slice(0, -1)
    .filter((folder) => !NUMBERED_FOLDER.test(folder.trim()))
    .slice(-2);
  return folders
    .map((folder) => folder.replace(/\s*[([]\d{4}[)\]]\s*$/, "").trim())
    .filter(Boolean)
    .join(" ");
}

/** "5" or "2, 3" or "2-4" as the numbers they stand for; null when it is not a list of numbers. */
export function parseParts(text: string): number[] | null {
  const numbers: number[] = [];
  for (const part of text.split(",").map((piece) => piece.trim()).filter(Boolean)) {
    const range = /^(\d+)\s*-\s*(\d+)$/.exec(part);
    if (range) {
      const [from, to] = [Number(range[1]), Number(range[2])];
      if (to < from || to - from > 60) return null;
      for (let n = from; n <= to; n++) numbers.push(n);
    } else if (/^\d+$/.test(part)) numbers.push(Number(part));
    else return null;
  }
  return numbers;
}

/** "Identify as…": search the library's sources for the work these files are and place them as it, whatever
 *  identification made of them. One form for every media type, inline or inside a dialog. */
export function IdentifyAs({
  categoryId,
  guess,
  busy,
  failed,
  onPlace,
}: {
  categoryId: number;
  /** The search to start with, queryFromPath of the files' path as a rule. */
  guess: string;
  busy: boolean;
  failed: string | null;
  onPlace: (choice: IdentityChoice) => void;
}) {
  const library = useLibrary(String(categoryId));
  const info = useMediaTypes().find((type) => type.key === library?.media_type);
  const words = vocabularyOf(info);
  const kind = info?.add_kinds[0]?.key;
  // A type whose parts are wanted one by one keeps one part to a file, so a person may say which.
  const placesParts = info?.wanted_grain === "part";

  const [typed, setTyped] = useState(guess);
  const query = useDebouncedValue(typed.trim(), 300);
  const [chosen, setChosen] = useState<{ provider: string; external_id: string; title: string } | null>(null);
  const [group, setGroup] = useState("");
  const [parts, setParts] = useState("");

  const results = useQuery({
    queryKey: ["identify-as", categoryId, kind, query],
    enabled: kind !== undefined && query.length >= 2,
    queryFn: () => ok(api.GET("/works/lookup", { params: { query: { category_id: categoryId, q: query, kind, limit: 15 } } })),
  });
  const parsed = parseParts(parts);
  const explicit = placesParts && parts.trim() !== "";
  const input = "rounded-md border border-border-subtle bg-canvas px-2 py-1 text-sm text-ink";

  return (
    <section className="flex min-h-0 flex-col gap-3 text-sm">
      <h2 className="text-sm font-medium">Which {words.work} is this?</h2>
      <label className="relative flex items-center">
        <Search aria-hidden className="pointer-events-none absolute left-2 size-3.5 text-ink-muted" />
        <input
          autoFocus
          value={typed}
          onChange={(event) => setTyped(event.target.value)}
          aria-label={`Search for the ${words.work}`}
          className={`${input} w-full pl-7`}
        />
      </label>
      {results.isFetching && <p className="text-xs text-ink-muted">Searching…</p>}
      {results.isError && <p className="text-xs text-danger">The search failed. Try again in a moment.</p>}
      {results.data?.length === 0 && <p className="text-xs text-ink-muted">Nothing found for “{query}”.</p>}
      <ul className="flex min-h-0 flex-col gap-1 overflow-y-auto">
        {(results.data ?? []).map((candidate) => {
          const picked = chosen?.provider === candidate.provider && chosen.external_id === candidate.external_id;
          return (
            <li key={`${candidate.provider}:${candidate.external_id}`}>
              <button
                type="button"
                onClick={() => setChosen({ provider: candidate.provider, external_id: candidate.external_id, title: candidate.title })}
                className={`flex w-full items-center gap-2 rounded-md border px-2 py-1 text-left ${picked ? "border-accent bg-accent/10" : "border-border-subtle hover:bg-canvas"}`}
              >
                {candidate.poster_url && <img src={remoteImage(candidate.poster_url, "thumb")} alt="" className="h-10 w-7 rounded object-cover" />}
                <span className="min-w-0 flex-1 truncate">
                  {candidate.title}
                  <span className="ml-2 text-xs text-ink-muted">
                    {[candidate.subtitle, candidate.year].filter(Boolean).join(" · ")}
                    {candidate.library_id != null ? " · already in the library" : ""}
                  </span>
                </span>
              </button>
            </li>
          );
        })}
      </ul>
      {placesParts && (
        <div className="flex flex-wrap items-end gap-3 text-xs text-ink-muted">
          <label className="flex flex-col gap-1">
            {words.Group}
            <input value={group} onChange={(event) => setGroup(event.target.value.replace(/\D/g, ""))} placeholder="from the name" className={`${input} w-32`} />
          </label>
          <label className="flex flex-col gap-1">
            {words.Parts}
            <input value={parts} onChange={(event) => setParts(event.target.value)} placeholder="from the name, or 3 or 3-4" className={`${input} w-48`} />
          </label>
          <span className="max-w-md">
            Leave both empty when the file name says which {words.part} it is.
          </span>
        </div>
      )}
      <div className="flex items-center gap-3">
        <button
          type="button"
          disabled={!chosen || busy || (explicit && !parsed)}
          onClick={() =>
            chosen &&
            onPlace({
              provider: chosen.provider,
              external_id: chosen.external_id,
              ...(explicit && parsed ? { group: Number(group || 1), parts: parsed } : {}),
            })
          }
          className="rounded-md bg-accent px-3 py-1.5 text-accent-ink disabled:opacity-50"
        >
          {busy ? "Placing…" : chosen ? `Place it as ${chosen.title}` : `Choose a ${words.work}`}
        </button>
        {failed && <span className="text-xs text-danger">{failed}</span>}
      </div>
    </section>
  );
}
