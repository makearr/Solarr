import { useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";

import { formatBytes } from "./Queue";

/** Tracks this library holds more than once, found by artist, title and length and not by their bytes: a flac and a
 *  96 kbit mp3 of one song are one track twice. Each group says which copy to keep, the one closer to the library's
 *  target quality. A report only: nothing is removed from here. */
export function Duplicates() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const report = useQuery({
    queryKey: ["duplicates", categoryId],
    enabled: categoryId !== undefined,
    queryFn: async () => {
      const { data, error } = await api.GET("/categories/{id}/duplicates", { params: { path: { id: Number(categoryId) } } });
      if (error) throw error;
      return data;
    },
  });
  const data = report.data;
  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-4 p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">Duplicates</h1>
        <p className="text-sm text-ink-muted">
          The same recording stored more than once: the same artist and title, lengths within a few seconds, in any format and
          on any album. The copy to keep is the one closer to this library's target quality. This only looks; nothing is
          removed. Files the probe has not measured yet are not compared.
        </p>
      </header>
      {report.isLoading && <p className="text-sm text-ink-muted">Looking…</p>}
      {report.error !== null && !report.isLoading && <p className="text-sm text-danger">The report could not be made.</p>}
      {data && data.groups.length === 0 && <p className="text-sm text-ink-muted">No duplicate tracks found.</p>}
      {data && data.groups.length > 0 && (
        <>
          <p className="text-sm">
            {data.groups.length} {data.groups.length === 1 ? "track" : "tracks"} held more than once; dropping the extra copies
            would free <span className="font-medium">{formatBytes(data.reclaimable_bytes)}</span>.
          </p>
          <ul className="flex flex-col gap-2">
            {data.groups.map((group, index) => (
              <li key={index} className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3">
                <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
                  <span className="text-sm font-medium">{group.title}</span>
                  <span className="text-xs text-ink-muted">{group.artist}</span>
                  <span className="ml-auto text-xs text-ink-muted">frees {formatBytes(group.reclaimable_bytes)}</span>
                </div>
                <ul className="flex flex-col gap-1.5">
                  {group.copies.map((copy) => (
                    <li
                      key={copy.media_object_id}
                      className={`flex flex-col gap-0.5 rounded-md border px-2.5 py-1.5 text-xs ${
                        copy.keep ? "border-accent bg-accent/5" : "border-border-subtle"
                      }`}
                    >
                      <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5">
                        <span className="rounded-full border border-border-subtle px-1.5 uppercase">{copy.audio_codec || "?"}</span>
                        <span>{copy.lossless ? "lossless" : copy.bitrate_kbps > 0 ? `${copy.bitrate_kbps} kbit` : "bitrate unknown"}</span>
                        <span className="text-ink-muted">{formatBytes(copy.size_bytes)}</span>
                        <span className="text-ink-muted">{formatDuration(copy.duration_ms)}</span>
                        <span className="text-ink-muted">on {copy.album}</span>
                        {copy.keep && <span className="ml-auto font-medium text-accent">Keep</span>}
                      </div>
                      <span className="break-all font-mono text-ink-muted">{copy.paths.join("  =  ")}</span>
                    </li>
                  ))}
                </ul>
                <p className="text-xs text-ink-muted">Keep: {group.keep_reason}.</p>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

function formatDuration(ms: number): string {
  const seconds = Math.round(ms / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}
