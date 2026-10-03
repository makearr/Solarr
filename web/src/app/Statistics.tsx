import { Link, useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { useLibrary } from "@/lib/useLibrary";

import { formatBytes } from "./Queue";

const OUTCOME_LABELS: Record<string, string> = {
  ACCEPT: "Acceptable",
  REJECT: "Rejected",
  DEFER: "Waiting for facts",
};

const STATE_LABELS: Record<string, string> = {
  COMPLETE: "Complete",
  PARTIAL: "Partial",
  MISSING: "Missing",
  UPGRADABLE: "Below cutoff",
  IMPORTED: "Imported",
  CLEANED: "Imported and cleaned up",
  IMPORTING: "Importing",
  FAILED: "Failed",
  SENT: "In progress",
  PREPARED: "Prepared",
};

type FileFigures = components["schemas"]["StatisticsFiles"][];

/** A bar per value of one measured attribute (video codec, resolution, HDR, audio): files and bytes, biggest first. */
function Breakdown({ title, note, rows }: { title: string; note: string; rows: FileFigures | undefined }) {
  if (!rows || rows.length === 0) return null;
  const total = rows.reduce((sum, row) => sum + row.bytes, 0);
  const files = rows.reduce((sum, row) => sum + row.files, 0);
  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-sm font-medium">{title}</h2>
      <p className="text-xs text-ink-muted">
        {files} files, {formatBytes(total)}. {note}
      </p>
      <ul className="flex flex-col gap-1.5">
        {[...rows]
          .sort((a, b) => b.bytes - a.bytes)
          .map((row) => (
            <li key={row.key} className="flex items-center gap-3 text-sm">
              <span className="w-28 shrink-0 truncate uppercase" title={row.key}>
                {row.key}
              </span>
              <span className="relative h-3 flex-1 overflow-hidden rounded-full bg-canvas">
                <span className="absolute inset-y-0 left-0 rounded-full bg-accent" style={{ width: `${total > 0 ? Math.max(1, Math.round((row.bytes / total) * 100)) : 0}%` }} />
              </span>
              <span className="w-44 shrink-0 text-right text-xs tabular-nums text-ink-muted">
                {row.files} files · {formatBytes(row.bytes)}
              </span>
            </li>
          ))}
      </ul>
    </section>
  );
}

/** Where a figure leads: the filtered view that shows exactly those items. */
function stateLink(categoryId: string, state: string): { to: string; search?: Record<string, string> } {
  if (state === "COMPLETE") return { to: `/library/${categoryId}/albums` };
  if (state === "UPGRADABLE") return { to: `/library/${categoryId}/wanted`, search: { view: "cutoff" } };
  return { to: `/library/${categoryId}/wanted` };
}

/** One library's statistics (section 21.12), read from the rollup the background job computed: composition,
 *  completeness and the last month's downloads. Each completeness figure opens exactly those albums. */
export function Statistics() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const stats = useQuery({
    queryKey: ["statistics", categoryId],
    enabled: categoryId !== undefined,
    queryFn: async () => {
      const { data, error } = await api.GET("/categories/{id}/statistics", { params: { path: { id: Number(categoryId) } } });
      if (error) throw error;
      return data;
    },
  });
  const data = stats.data;
  const video = useLibrary(categoryId ?? "")?.media_type === "tv";
  const totalFiles = (data?.files_by_codec ?? []).reduce((sum, row) => sum + row.files, 0);
  const totalBytes = (data?.files_by_codec ?? []).reduce((sum, row) => sum + row.bytes, 0);
  const growth = data?.growth_by_month ?? [];
  const largestMonth = Math.max(1, ...growth.map((row) => row.bytes));
  const decisions = (data?.decisions_by_outcome ?? []).reduce((sum, row) => sum + row.count, 0);
  const albums = (data?.albums_by_state ?? []).reduce((sum, row) => sum + row.count, 0);

  return (
    <div className="mx-auto flex h-full max-w-4xl flex-col gap-6 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">Statistics</h1>
        <p className="text-sm text-ink-muted">
          {data?.computed_at
            ? `As of ${new Date(data.computed_at).toLocaleString()}. Computed once a day in the background; nothing here is counted live.`
            : "Not computed yet. It runs once a day in the background, and under Settings → Scheduler → Statistics on demand."}
        </p>
      </header>
      {stats.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {stats.error !== null && <p className="text-sm text-danger">The statistics could not be read.</p>}

      {data && (
        <>
          <section className="flex flex-col gap-2">
            <h2 className="text-sm font-medium">Completeness</h2>
            <p className="text-xs text-ink-muted">
              {albums} monitored {video ? "seasons" : "albums"}. {video ? "" : "Each figure opens those albums."}
            </p>
            <ul className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
              {data.albums_by_state.map((row) => {
                const target = stateLink(categoryId ?? "", row.key);
                return (
                  <li key={row.key}>
                    <Link
                      to={target.to}
                      search={target.search}
                      className="flex flex-col rounded-lg border border-border-subtle bg-surface p-3 hover:border-accent"
                    >
                      <span className="text-2xl font-semibold tabular-nums">{row.count}</span>
                      <span className="text-xs text-ink-muted">{STATE_LABELS[row.key] ?? row.key}</span>
                    </Link>
                  </li>
                );
              })}
              {data.albums_by_state.length === 0 && <li className="text-sm text-ink-muted">No monitored albums.</li>}
            </ul>
          </section>

          {video && (
            <>
              <Breakdown title="Resolution" note="As the probe measured each file." rows={data.files_by_resolution} />
              <Breakdown title="Video codec" note="As the probe measured each file." rows={data.files_by_video_codec} />
              <Breakdown title="HDR" note="Files without an HDR format count as sdr." rows={data.files_by_hdr} />
              <Breakdown title="Audio" note="Codec and channel layout of the first audio track." rows={data.files_by_audio} />
            </>
          )}
          <section className="flex flex-col gap-2">
            <h2 className="text-sm font-medium">{video ? "Audio codecs (by file)" : "Composition"}</h2>
            <p className="text-xs text-ink-muted">
              {totalFiles} files, {formatBytes(totalBytes)}, by audio codec. Files the probe has not measured yet are
              unknown.
            </p>
            <ul className="flex flex-col gap-1.5">
              {[...data.files_by_codec].sort((a, b) => b.bytes - a.bytes).map((row) => (
                <li key={row.key} className="flex items-center gap-3 text-sm">
                  <span className="w-20 shrink-0 uppercase">{row.key}</span>
                  <span className="relative h-3 flex-1 overflow-hidden rounded-full bg-canvas">
                    <span
                      className="absolute inset-y-0 left-0 rounded-full bg-accent"
                      style={{ width: `${totalBytes > 0 ? Math.max(1, Math.round((row.bytes / totalBytes) * 100)) : 0}%` }}
                    />
                  </span>
                  <span className="w-44 shrink-0 text-right text-xs tabular-nums text-ink-muted">
                    {row.files} files · {formatBytes(row.bytes)}
                  </span>
                </li>
              ))}
              {data.files_by_codec.length === 0 && <li className="text-sm text-ink-muted">No files yet.</li>}
            </ul>
          </section>

          <section className="flex flex-col gap-2">
            <h2 className="text-sm font-medium">Growth</h2>
            <p className="text-xs text-ink-muted">
              Files by the month Solarr first saw them. The first scan of an existing library lands in one month.
            </p>
            <ul className="flex flex-col gap-1.5">
              {growth.map((row) => (
                <li key={row.key} className="flex items-center gap-3 text-sm">
                  <span className="w-20 shrink-0 tabular-nums">{row.key}</span>
                  <span className="relative h-3 flex-1 overflow-hidden rounded-full bg-canvas">
                    <span
                      className="absolute inset-y-0 left-0 rounded-full bg-accent"
                      style={{ width: `${Math.max(1, Math.round((row.bytes / largestMonth) * 100))}%` }}
                    />
                  </span>
                  <span className="w-44 shrink-0 text-right text-xs tabular-nums text-ink-muted">
                    {row.files} files · {formatBytes(row.bytes)}
                  </span>
                </li>
              ))}
              {growth.length === 0 && <li className="text-sm text-ink-muted">No files yet.</li>}
            </ul>
          </section>

          <section className="flex flex-col gap-2">
            <h2 className="text-sm font-medium">Release decisions, last {data.window_days} days</h2>
            <p className="text-xs text-ink-muted">
              {decisions} releases judged against your profiles, whichever of them was then downloaded.
            </p>
            <ul className="grid gap-2 sm:grid-cols-3">
              {data.decisions_by_outcome.map((row) => (
                <li key={row.key} className="flex flex-col rounded-lg border border-border-subtle bg-surface p-3">
                  <span className="text-2xl font-semibold tabular-nums">{row.count}</span>
                  <span className="text-xs text-ink-muted">{OUTCOME_LABELS[row.key] ?? row.key}</span>
                </li>
              ))}
              {data.decisions_by_outcome.length === 0 && <li className="text-sm text-ink-muted">No releases judged in this window.</li>}
            </ul>
          </section>

          <section className="flex flex-col gap-2">
            <h2 className="text-sm font-medium">Acquisition, last {data.window_days} days</h2>
            <ul className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
              {data.downloads_by_state.map((row) => (
                <li key={row.key}>
                  <Link
                    to="/activity/history"
                    className="flex flex-col rounded-lg border border-border-subtle bg-surface p-3 hover:border-accent"
                  >
                    <span className="text-2xl font-semibold tabular-nums">{row.count}</span>
                    <span className="text-xs text-ink-muted">{STATE_LABELS[row.key] ?? row.key}</span>
                  </Link>
                </li>
              ))}
              {data.downloads_by_state.length === 0 && <li className="text-sm text-ink-muted">No downloads in this window.</li>}
            </ul>
          </section>
        </>
      )}
    </div>
  );
}
