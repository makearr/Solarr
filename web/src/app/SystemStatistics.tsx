import { useNavigate, useSearch } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { useCategories } from "@/lib/resources";
import { t, type MessageKey } from "@/lib/i18n";
import { PageHeader } from "@/ui/PageHeader";

import { Gauge, MetricChart, RANGES, formatValue, useLatest, type Format, type Line, type RangeKey } from "./SystemChart";

const TABS = ["overview", "libraries", "activity", "process", "disks", "network"] as const;
type TabKey = (typeof TABS)[number];
/** Manage > Statistics: what Solarr itself measured, minute by minute. */
export function SystemStatistics() {
  const search = useSearch({ strict: false }) as { tab?: string; range?: string };
  const navigate = useNavigate();
  const tab: TabKey = (TABS as readonly string[]).includes(search.tab ?? "") ? (search.tab as TabKey) : "overview";
  const range: RangeKey = (RANGES as readonly string[]).includes(search.range ?? "") ? (search.range as RangeKey) : "day";
  const categories = useCategories();
  const scopes = useQuery({ queryKey: ["system-metric-scopes"], queryFn: () => ok(api.GET("/statistics/system/scopes")), refetchInterval: 120_000 });
  const scopesOf = (series: string) => (scopes.data?.scopes ?? []).filter((row) => row.series === series && row.scope !== "").map((row) => row.scope);
  const go = (next: Partial<{ tab: string; range: string }>) =>
    navigate({ to: "/statistics", search: (previous: Record<string, unknown>) => ({ ...previous, ...next }) as never });

  const libraries = scopesOf("library.items");
  const diskGroups = scopesOf("diskgroup.latency_ms");
  const watchGroups = scopesOf("diskgroup.watches");
  const nameOf = (id: string) => categories.data?.find((category) => String(category.id) === id)?.name ?? id;

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-4 p-6">
      <PageHeader title={t("nav.statistics")} />
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div role="tablist" className="flex flex-wrap gap-1 border-b border-border-subtle text-sm">
          {TABS.map((key) => (
            <button
              key={key}
              role="tab"
              aria-selected={key === tab}
              onClick={() => go({ tab: key })}
              className={`-mb-px border-b-2 px-3 py-1.5 ${key === tab ? "border-accent font-medium" : "border-transparent text-ink-muted hover:text-ink"}`}
            >
              {t(`systemStatistics.tab.${key}` as MessageKey)}
            </button>
          ))}
        </div>
        <select
          aria-label={t("systemStatistics.range")}
          className="rounded border border-border-subtle bg-surface px-2 py-1 text-xs"
          value={range}
          onChange={(event) => go({ range: event.target.value })}
        >
          {RANGES.map((key) => (
            <option key={key} value={key}>
              {t(`systemStatistics.range.${key}` as MessageKey)}
            </option>
          ))}
        </select>
      </div>

      {tab === "overview" && (
        <>
          <Gauges />
          <Charts
            range={range}
            specs={[
              single(t("systemStatistics.cpu"), "process.cpu_percent", "percent"),
              single(t("systemStatistics.memory"), "process.resident_bytes", "bytes"),
              single(t("systemStatistics.scansPerMin"), "jobs.scans_per_min", "number"),
              single(t("systemStatistics.metadataPerMin"), "jobs.metadata_per_min", "number"),
              {
                title: t("systemStatistics.bandwidth"), format: "rate",
                lines: [{ label: t("systemStatistics.ownCalls"), series: "net.bytes_per_sec" }, { label: t("systemStatistics.downloadClients"), series: "downloads.bytes_per_sec" }],
              },
              single(t("systemStatistics.internetLatency"), "net.latency_ms", "ms"),
            ]}
          />
        </>
      )}

      {tab === "libraries" &&
        (libraries.length === 0 ? (
          <p className="text-xs text-ink-muted">{t("systemStatistics.noData")}</p>
        ) : (
          <Charts
            range={range}
            specs={[
              { title: t("systemStatistics.items"), format: "number", lines: libraries.map((id) => ({ label: nameOf(id), series: "library.items", scope: id })) },
              { title: t("systemStatistics.files"), format: "number", lines: libraries.map((id) => ({ label: nameOf(id), series: "library.files", scope: id })) },
              { title: t("systemStatistics.storage"), format: "bytes", lines: libraries.map((id) => ({ label: nameOf(id), series: "library.bytes", scope: id })) },
            ]}
          />
        ))}

      {tab === "activity" && (
        <Charts
          range={range}
          specs={[
            single(t("systemStatistics.scansPerMin"), "jobs.scans_per_min", "number"),
            single(t("systemStatistics.metadataPerMin"), "jobs.metadata_per_min", "number"),
          ]}
        />
      )}

      {tab === "process" && (
        <Charts
          range={range}
          specs={[
            single(t("systemStatistics.cpu"), "process.cpu_percent", "percent"),
            single(t("systemStatistics.memory"), "process.resident_bytes", "bytes"),
            single(t("systemStatistics.ioRead"), "process.read_bytes_per_sec", "rate"),
            single(t("systemStatistics.ioWrite"), "process.write_bytes_per_sec", "rate"),
            single(t("systemStatistics.diskRead"), "process.disk_read_bytes_per_sec", "rate"),
            single(t("systemStatistics.diskWrite"), "process.disk_write_bytes_per_sec", "rate"),
          ]}
        />
      )}

      {tab === "disks" &&
        (diskGroups.length === 0 ? (
          <p className="text-xs text-ink-muted">{t("systemStatistics.noData")}</p>
        ) : (
          <>
            <p className="text-xs text-ink-muted">{t("systemStatistics.disksNote")}</p>
            <WatchFigures groups={watchGroups} />
            <Charts
              range={range}
              specs={[
                { title: t("systemStatistics.diskLatency"), format: "ms", lines: groupLines(diskGroups, "diskgroup.latency_ms") },
                { title: t("systemStatistics.diskThroughput"), format: "rate", lines: groupLines(diskGroups, "diskgroup.bytes_per_sec") },
                { title: t("systemStatistics.diskCalls"), format: "number", lines: groupLines(diskGroups, "diskgroup.calls_per_min") },
                { title: t("systemStatistics.diskLoad"), format: "number", lines: groupLines(diskGroups, "diskgroup.degradation") },
                { title: t("systemStatistics.watches"), format: "number", lines: groupLines(watchGroups, "diskgroup.watches") },
              ]}
            />
          </>
        ))}

      {tab === "network" && (
        <>
          <Charts
            range={range}
            specs={[
              single(t("systemStatistics.bandwidth"), "net.bytes_per_sec", "rate"),
              {
                title: t("systemStatistics.downloadSpeed"), format: "rate",
                lines: [{ label: t("systemStatistics.allClients"), series: "downloads.bytes_per_sec" }, ...scopesOf("downloads.bytes_per_sec").map((client) => ({ label: client, series: "downloads.bytes_per_sec", scope: client }))],
              },
              single(t("systemStatistics.internetLatency"), "net.latency_ms", "ms"),
            ]}
          />
          {scopesOf("net.latency_ms").map((provider) => (
            <div key={provider} className="flex flex-col gap-2">
              <h2 className="font-mono text-sm">{provider}</h2>
              <Charts
                range={range}
                specs={[
                  { title: t("systemStatistics.providerLatency"), format: "ms", lines: [{ label: provider, series: "net.latency_ms", scope: provider }] },
                  { title: t("systemStatistics.providerThroughput"), format: "rate", lines: [{ label: provider, series: "net.bytes_per_sec", scope: provider }] },
                ]}
              />
            </div>
          ))}
        </>
      )}
    </div>
  );
}

interface ChartSpec {
  title: string;
  lines: Line[];
  format: Format;
}

const single = (title: string, series: string, format: Format): ChartSpec => ({ title, format, lines: [{ label: title, series }] });
const groupLines = (groups: readonly string[], series: string): Line[] => groups.map((group) => ({ label: group, series, scope: group }));

function Charts({ range, specs }: { range: RangeKey; specs: ChartSpec[] }) {
  return (
    <div className="grid gap-3 lg:grid-cols-2">
      {specs.map((spec) => (
        <MetricChart key={spec.title} range={range} {...spec} />
      ))}
    </div>
  );
}

/** How many inotify watches each disk holds right now. */
function WatchFigures({ groups }: { groups: readonly string[] }) {
  if (groups.length === 0) return null;
  return (
    <ul className="flex flex-wrap gap-2 text-sm">
      {groups.map((group) => (
        <WatchFigure key={group} group={group} />
      ))}
    </ul>
  );
}

function WatchFigure({ group }: { group: string }) {
  const latest = useLatest("diskgroup.watches", group, "hour");
  return (
    <li className="rounded-lg border border-border-subtle bg-surface px-3 py-2">
      <span className="font-mono text-xs text-ink-muted">{group}</span>
      <span className="block text-lg font-semibold tabular-nums">{latest === undefined ? "-" : formatValue(latest.latest, "number")}</span>
      <span className="text-xs text-ink-muted">{t("systemStatistics.watchesHeld")}</span>
    </li>
  );
}

/** What is in use right now, against the CPU's ceiling or the busiest minute of the last day. */
function Gauges() {
  const cpu = useLatest("process.cpu_percent", "", "hour");
  const memory = { now: useLatest("process.resident_bytes", "", "hour"), day: useLatest("process.resident_bytes", "", "day") };
  const read = { now: useLatest("process.disk_read_bytes_per_sec", "", "hour"), day: useLatest("process.disk_read_bytes_per_sec", "", "day") };
  const write = { now: useLatest("process.disk_write_bytes_per_sec", "", "hour"), day: useLatest("process.disk_write_bytes_per_sec", "", "day") };
  const network = { now: useLatest("net.bytes_per_sec", "", "hour"), day: useLatest("net.bytes_per_sec", "", "day") };
  const downloads = { now: useLatest("downloads.bytes_per_sec", "", "hour"), day: useLatest("downloads.bytes_per_sec", "", "day") };
  const note = t("systemStatistics.gaugeNote");
  const storage = read.now === undefined && write.now === undefined ? undefined : (read.now?.latest ?? 0) + (write.now?.latest ?? 0);
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
      <Gauge label={t("systemStatistics.gaugeCpu")} value={cpu?.latest} max={Math.max(100, cpu?.peak ?? 0)} format="percent" />
      <Gauge label={t("systemStatistics.gaugeMemory")} value={memory.now?.latest} max={memory.day?.peak ?? 0} format="bytes" note={note} />
      <Gauge label={t("systemStatistics.gaugeStorage")} value={storage} max={(read.day?.peak ?? 0) + (write.day?.peak ?? 0)} format="rate" note={note} />
      <Gauge label={t("systemStatistics.gaugeNetwork")} value={network.now?.latest} max={network.day?.peak ?? 0} format="rate" note={note} />
      <Gauge label={t("systemStatistics.gaugeDownloads")} value={downloads.now?.latest} max={downloads.day?.peak ?? 0} format="rate" note={note} />
    </div>
  );
}
