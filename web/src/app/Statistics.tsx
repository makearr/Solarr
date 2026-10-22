import { Link, useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { vocabularyOf } from "@/lib/i18n";
import { useLibrary } from "@/lib/useLibrary";
import { useMediaTypes } from "@/lib/useMediaTypes";

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
              <span className="w-20 shrink-0 truncate uppercase sm:w-28" title={row.key}>
                {row.key}
              </span>
              <span className="relative h-3 min-w-8 flex-1 overflow-hidden rounded-full bg-canvas">
                <span className="absolute inset-y-0 left-0 rounded-full bg-accent" style={{ width: `${total > 0 ? Math.max(1, Math.round((row.bytes / total) * 100)) : 0}%` }} />
              </span>
              <span className="shrink-0 text-right text-xs tabular-nums text-ink-muted sm:w-44">
                {row.files} files · {formatBytes(row.bytes)}
              </span>
            </li>
          ))}
      </ul>
    </section>
  );
}

type WorkFigures = components["schemas"]["StatisticsSeries"][];

/** The works that take most space, or have most parts missing, each a link to its page. */
function WorkList({ title, note, rows, categoryId, gaps }: { title: string; note: string; rows: WorkFigures | undefined; categoryId: string; gaps?: boolean }) {
  if (!rows || rows.length === 0) return null;
  const largest = Math.max(1, ...rows.map((row) => (gaps ? (row.missing ?? 0) : (row.bytes ?? 0))));
  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-sm font-medium">{title}</h2>
      <p className="text-xs text-ink-muted">{note}</p>
      <ul className="flex flex-col gap-1.5">
        {rows.map((row) => {
          const value = gaps ? (row.missing ?? 0) : (row.bytes ?? 0);
          return (
            <li key={row.series_id} className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm sm:flex-nowrap">
              <Link to="/library/$categoryId/works/$workId" params={{ categoryId, workId: String(row.series_id) }} className="w-full shrink-0 truncate hover:underline sm:w-56" title={row.title}>
                {row.title}
              </Link>
              <span className="relative h-3 min-w-8 flex-1 overflow-hidden rounded-full bg-canvas">
                <span className="absolute inset-y-0 left-0 rounded-full bg-accent" style={{ width: `${Math.max(1, Math.round((value / largest) * 100))}%` }} />
              </span>
              <span className="shrink-0 text-right text-xs tabular-nums text-ink-muted sm:w-52">
                {gaps
                  ? `${row.missing ?? 0} missing${(row.unaired ?? 0) > 0 ? ` · ${row.unaired} to come` : ""}`
                  : `${row.files ?? 0} files · ${formatBytes(row.bytes ?? 0)}`}
              </span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

/** Where a figure leads: the filtered view that shows exactly those items. */
function stateLink(categoryId: string, worksLevel: string, state: string): { to: string; search?: Record<string, string> } {
  if (state === "COMPLETE") return { to: `/library/${categoryId}/${worksLevel}` };
  if (state === "UPGRADABLE") return { to: `/library/${categoryId}/wanted`, search: { view: "cutoff" } };
  return { to: `/library/${categoryId}/wanted` };
}

/** One library's statistics (section 21.12), read from the rollup the background job computed: composition,
 *  completeness and the last month's downloads, in the words of the library's type. */
export function Statistics() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const stats = useQuery({
    queryKey: ["statistics", categoryId],
    enabled: categoryId !== undefined,
    queryFn: () => ok(api.GET("/categories/{id}/statistics", { params: { path: { id: Number(categoryId) } } })),
  });
  const data = stats.data;
  const library = useLibrary(categoryId ?? "");
  const info = useMediaTypes().find((type) => type.key === library?.media_type);
  const words = vocabularyOf(info);
  const worksLevel = library?.levels.find((level) => level.entity === "work")?.key ?? "works";
  // A type wanted part by part keeps one wanted item per group (Sonarr's seasons); others one per work.
  const items = info?.wanted_grain === "part" ? words.groups : words.works;
  // The picture figures are measured only for video, so their presence says the library holds some.
  const video = (data?.files_by_video_codec ?? []).length > 0;
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
              {albums} monitored {items}. Each figure opens those {items}.
            </p>
            <ul className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
              {data.albums_by_state.map((row) => {
                const target = stateLink(categoryId ?? "", worksLevel, row.key);
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
              {data.albums_by_state.length === 0 && <li className="text-sm text-ink-muted">No monitored {items}.</li>}
            </ul>
          </section>

          {video && (
            <>
              <Breakdown title="Resolution" note="As the probe measured each file." rows={data.files_by_resolution} />
              <Breakdown title="Video codec" note="As the probe measured each file." rows={data.files_by_video_codec} />
              <Breakdown title="HDR" note="Files without an HDR format count as sdr." rows={data.files_by_hdr} />
              <Breakdown title="Audio" note="Codec and channel layout of the first audio track." rows={data.files_by_audio} />
              <section className="flex flex-col gap-2">
                {(data.size_per_hour ?? []).length > 0 && (
                  <>
                    <h2 className="text-sm font-medium">Size and bitrate per hour</h2>
                    <p className="text-xs text-ink-muted">Bytes per hour of picture and the average video bitrate: a file far above its neighbours is bloated, far below starved.</p>
                    <ul className="flex flex-col gap-1.5">
                      {(data.size_per_hour ?? []).map((row) => (
                        <li key={row.key} className="flex items-center gap-3 text-sm">
                          <span className="w-28 shrink-0 uppercase">{row.key}</span>
                          <span className="flex-1 text-xs tabular-nums text-ink-muted">
                            {formatBytes(row.bytes_per_hour)} per hour · {row.video_kbps > 0 ? `${(row.video_kbps / 1000).toFixed(1)} Mbit/s video` : "bitrate unknown"}
                          </span>
                          <span className="w-24 shrink-0 text-right text-xs tabular-nums text-ink-muted">{row.files} files</span>
                        </li>
                      ))}
                    </ul>
                  </>
                )}
              </section>
            </>
          )}
          <WorkList title={`Biggest ${words.works}`} note={`The 20 ${words.works} taking most space, each file counted once.`} rows={data.series_by_size} categoryId={categoryId ?? ""} />
          <WorkList
            title="Gaps"
            note={`Monitored ${words.parts} without a file: the ones already out are missing, the others are still to come.`}
            rows={data.series_gaps}
            categoryId={categoryId ?? ""}
            gaps
          />
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
                  <span className="relative h-3 min-w-8 flex-1 overflow-hidden rounded-full bg-canvas">
                    <span
                      className="absolute inset-y-0 left-0 rounded-full bg-accent"
                      style={{ width: `${totalBytes > 0 ? Math.max(1, Math.round((row.bytes / totalBytes) * 100)) : 0}%` }}
                    />
                  </span>
                  <span className="shrink-0 text-right text-xs tabular-nums text-ink-muted sm:w-44">
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
                  <span className="relative h-3 min-w-8 flex-1 overflow-hidden rounded-full bg-canvas">
                    <span
                      className="absolute inset-y-0 left-0 rounded-full bg-accent"
                      style={{ width: `${Math.max(1, Math.round((row.bytes / largestMonth) * 100))}%` }}
                    />
                  </span>
                  <span className="shrink-0 text-right text-xs tabular-nums text-ink-muted sm:w-44">
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

          <Searching
            windowDays={data.window_days}
            searches={data.searches}
            mostSearched={data.most_searched}
            categoryId={categoryId ?? ""}
            group={words.Group ?? "Group"}
          />

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

/** How well searching works: how often a search finds a release the library accepts, and what keeps not being found. */
function Searching({
  windowDays,
  searches,
  mostSearched,
  categoryId,
  group,
}: {
  windowDays: number;
  searches: components["schemas"]["StatisticsSearches"] | undefined;
  mostSearched: components["schemas"]["StatisticsSearchedItem"][] | undefined;
  categoryId: string;
  group: string;
}) {
  const searched = searches?.searched ?? 0;
  const found = searches?.found ?? 0;
  const tiles: [string, string][] = [
    [String(searched), "Searches"],
    [String(found), "Found something"],
    [searched > 0 ? `${Math.round((found / searched) * 100)}%` : "–", "Found rate"],
    [found > 0 ? (searched / found).toFixed(1) : "–", "Searches per find"],
  ];
  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-sm font-medium">Searching, last {windowDays} days</h2>
      <p className="text-xs text-ink-muted">
        Automatic and manual searches; one found something when it turned up a release your profile accepts.
      </p>
      <ul className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
        {tiles.map(([value, label]) => (
          <li key={label} className="flex flex-col rounded-lg border border-border-subtle bg-surface p-3">
            <span className="text-2xl font-semibold tabular-nums">{value}</span>
            <span className="text-xs text-ink-muted">{label}</span>
          </li>
        ))}
      </ul>
      <h3 className="mt-2 text-sm font-medium">Searched most, still missing</h3>
      {mostSearched === undefined || mostSearched.length === 0 ? (
        <p className="text-sm text-ink-muted">Nothing has been searched for twice without being found.</p>
      ) : (
        <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
          {mostSearched.map((item) => (
            <li key={`${item.work_id}:${item.media_ref ?? ""}`} className="flex items-center gap-3 px-3 py-2 text-sm">
              <Link
                to="/library/$categoryId/works/$workId"
                params={{ categoryId, workId: String(item.work_id) }}
                className="min-w-0 flex-1 truncate hover:underline"
              >
                {item.lead ? `${item.lead} – ` : ""}
                {item.title}
                {item.media_ref ? <span className="text-ink-muted"> · {groupLabel(item.media_ref, group)}</span> : null}
              </Link>
              <span className="shrink-0 text-xs text-ink-muted tabular-nums">
                {item.searches} searches · last {new Date(item.last_searched_at).toLocaleDateString()}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/** groupLabel reads a group reference such as "s02" as "Season 2", in the library's own word for a group. */
function groupLabel(ref: string, group: string): string {
  const number = /^[a-z](\d+)$/.exec(ref)?.[1];
  return number === undefined ? ref : `${group} ${Number(number)}`;
}
