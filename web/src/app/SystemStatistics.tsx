import { useNavigate, useSearch } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { api, ok } from "@/api/client";
import { useCategories } from "@/lib/resources";
import { t, type MessageKey } from "@/lib/i18n";
import { PageHeader } from "@/ui/PageHeader";

import { formatBytes } from "./Queue";

const TABS = ["overview", "libraries", "activity", "process", "disks", "network"] as const;
type TabKey = (typeof TABS)[number];
const RANGES = ["hour", "6hours", "day", "week", "month"] as const;
type RangeKey = (typeof RANGES)[number];

type Format = "number" | "bytes" | "rate" | "ms" | "percent";

function formatValue(value: number, format: Format): string {
  switch (format) {
    case "bytes":
      return formatBytes(value);
    case "rate":
      return `${formatBytes(value)}/s`;
    case "ms":
      return `${value.toFixed(value < 10 ? 2 : 0)} ms`;
    case "percent":
      return `${value.toFixed(1)}%`;
    default:
      return Number.isInteger(value) ? String(value) : value.toFixed(1);
  }
}

const WIDTH = 640;
const HEIGHT = 120;

/** A line over time; the figures at the ends and the extremes are written out so the chart can be read without hovering. */
function Chart({ points, format, spanSeconds }: { points: { at: number; value: number }[]; format: Format; spanSeconds: number }) {
  const [hover, setHover] = useState<number | null>(null);
  if (points.length === 0) return <p className="py-6 text-center text-xs text-ink-muted">{t("systemStatistics.noData")}</p>;
  const end = points[points.length - 1]!.at;
  const start = end - spanSeconds;
  const max = Math.max(...points.map((point) => point.value), 0) || 1;
  const x = (at: number) => ((at - start) / spanSeconds) * WIDTH;
  const y = (value: number) => HEIGHT - (value / max) * (HEIGHT - 8) - 4;
  const line = points.map((point, index) => `${index === 0 ? "M" : "L"}${x(point.at).toFixed(1)},${y(point.value).toFixed(1)}`).join(" ");
  const area = `${line} L${x(end).toFixed(1)},${HEIGHT} L${x(points[0]!.at).toFixed(1)},${HEIGHT} Z`;
  const shown = hover === null ? points[points.length - 1]! : points[hover]!;
  return (
    <div className="flex flex-col gap-1">
      <svg
        viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
        role="img"
        aria-label={t("systemStatistics.chartLabel", { latest: formatValue(shown.value, format) })}
        className="h-28 w-full text-accent"
        preserveAspectRatio="none"
        onPointerLeave={() => setHover(null)}
        onPointerMove={(event) => {
          const box = event.currentTarget.getBoundingClientRect();
          const at = start + ((event.clientX - box.left) / box.width) * spanSeconds;
          let nearest = 0;
          points.forEach((point, index) => {
            if (Math.abs(point.at - at) < Math.abs(points[nearest]!.at - at)) nearest = index;
          });
          setHover(nearest);
        }}
      >
        <line x1="0" x2={WIDTH} y1={HEIGHT - 1} y2={HEIGHT - 1} className="stroke-border-subtle" strokeWidth="1" />
        <path d={area} fill="currentColor" opacity="0.12" />
        <path d={line} fill="none" stroke="currentColor" strokeWidth="1.5" vectorEffect="non-scaling-stroke" />
        {hover !== null && <line x1={x(shown.at)} x2={x(shown.at)} y1="0" y2={HEIGHT} stroke="currentColor" opacity="0.4" vectorEffect="non-scaling-stroke" />}
      </svg>
      <div className="flex justify-between text-xs tabular-nums text-ink-muted">
        <span>{new Date(shown.at * 1000).toLocaleString()}</span>
        <span className="text-ink">{formatValue(shown.value, format)}</span>
        <span>{t("systemStatistics.peak", { value: formatValue(max, format) })}</span>
      </div>
    </div>
  );
}

function SeriesChart({ title, series, scope = "", range, format }: { title: string; series: string; scope?: string; range: RangeKey; format: Format }) {
  const data = useQuery({
    queryKey: ["system-metric", series, scope, range],
    queryFn: () => ok(api.GET("/statistics/system/series", { params: { query: { series, scope, range } } })),
    refetchInterval: 60_000,
  });
  const span = { hour: 3600, "6hours": 21600, day: 86400, week: 604800, month: 2592000 }[range];
  return (
    <section className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface p-3">
      <h2 className="text-sm font-medium">{title}</h2>
      {data.isPending ? <p className="py-6 text-center text-xs text-ink-muted">{t("systemStatistics.loading")}</p> : <Chart points={data.data?.points ?? []} format={format} spanSeconds={span} />}
    </section>
  );
}

function Charts({ range, specs }: { range: RangeKey; specs: { title: string; series: string; scope?: string; format: Format }[] }) {
  return (
    <div className="grid gap-3 lg:grid-cols-2">
      {specs.map((spec) => (
        <SeriesChart key={`${spec.series}|${spec.scope ?? ""}`} range={range} {...spec} />
      ))}
    </div>
  );
}

/** Manage > Statistics: what Solarr itself measured, minute by minute. */
export function SystemStatistics() {
  const search = useSearch({ strict: false }) as { tab?: string; range?: string; scope?: string };
  const navigate = useNavigate();
  const tab: TabKey = (TABS as readonly string[]).includes(search.tab ?? "") ? (search.tab as TabKey) : "overview";
  const range: RangeKey = (RANGES as readonly string[]).includes(search.range ?? "") ? (search.range as RangeKey) : "day";
  const categories = useCategories();
  const scopes = useQuery({ queryKey: ["system-metric-scopes"], queryFn: () => ok(api.GET("/statistics/system/scopes")), refetchInterval: 120_000 });
  const scopesOf = (series: string) => (scopes.data?.scopes ?? []).filter((row) => row.series === series && row.scope !== "").map((row) => row.scope);
  const go = (next: Partial<{ tab: string; range: string; scope: string }>) =>
    navigate({ to: "/statistics", search: (previous: Record<string, unknown>) => ({ ...previous, ...next }) as never });

  const libraryId = search.scope && scopesOf("library.items").includes(search.scope) ? search.scope : scopesOf("library.items")[0];
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
        <Charts
          range={range}
          specs={[
            { title: t("systemStatistics.cpu"), series: "process.cpu_percent", format: "percent" },
            { title: t("systemStatistics.memory"), series: "process.resident_bytes", format: "bytes" },
            { title: t("systemStatistics.scansPerMin"), series: "jobs.scans_per_min", format: "number" },
            { title: t("systemStatistics.metadataPerMin"), series: "jobs.metadata_per_min", format: "number" },
            { title: t("systemStatistics.bandwidth"), series: "net.bytes_per_sec", format: "rate" },
            { title: t("systemStatistics.internetLatency"), series: "net.latency_ms", format: "ms" },
          ]}
        />
      )}

      {tab === "libraries" && (
        <>
          {libraryId === undefined ? (
            <p className="text-xs text-ink-muted">{t("systemStatistics.noData")}</p>
          ) : (
            <>
              <label className="flex items-center gap-2 text-xs">
                {t("systemStatistics.library")}
                <select
                  className="rounded border border-border-subtle bg-surface px-2 py-1"
                  value={libraryId}
                  onChange={(event) => go({ scope: event.target.value })}
                >
                  {scopesOf("library.items").map((id) => (
                    <option key={id} value={id}>
                      {nameOf(id)}
                    </option>
                  ))}
                </select>
              </label>
              <Charts
                range={range}
                specs={[
                  { title: t("systemStatistics.items"), series: "library.items", scope: libraryId, format: "number" },
                  { title: t("systemStatistics.files"), series: "library.files", scope: libraryId, format: "number" },
                  { title: t("systemStatistics.storage"), series: "library.bytes", scope: libraryId, format: "bytes" },
                ]}
              />
            </>
          )}
        </>
      )}

      {tab === "activity" && (
        <Charts
          range={range}
          specs={[
            { title: t("systemStatistics.scansPerMin"), series: "jobs.scans_per_min", format: "number" },
            { title: t("systemStatistics.metadataPerMin"), series: "jobs.metadata_per_min", format: "number" },
          ]}
        />
      )}

      {tab === "process" && (
        <Charts
          range={range}
          specs={[
            { title: t("systemStatistics.cpu"), series: "process.cpu_percent", format: "percent" },
            { title: t("systemStatistics.memory"), series: "process.resident_bytes", format: "bytes" },
            { title: t("systemStatistics.ioRead"), series: "process.read_bytes_per_sec", format: "rate" },
            { title: t("systemStatistics.ioWrite"), series: "process.write_bytes_per_sec", format: "rate" },
            { title: t("systemStatistics.diskRead"), series: "process.disk_read_bytes_per_sec", format: "rate" },
            { title: t("systemStatistics.diskWrite"), series: "process.disk_write_bytes_per_sec", format: "rate" },
          ]}
        />
      )}

      {tab === "disks" &&
        (scopesOf("disk.latency_ms").length === 0 ? (
          <p className="text-xs text-ink-muted">{t("systemStatistics.noData")}</p>
        ) : (
          scopesOf("disk.latency_ms").map((root) => (
            <div key={root} className="flex flex-col gap-2">
              <h2 className="font-mono text-sm">{root}</h2>
              <Charts
                range={range}
                specs={[
                  { title: t("systemStatistics.diskLatency"), series: "disk.latency_ms", scope: root, format: "ms" },
                  { title: t("systemStatistics.diskThroughput"), series: "disk.bytes_per_sec", scope: root, format: "rate" },
                  { title: t("systemStatistics.diskCalls"), series: "disk.calls_per_min", scope: root, format: "number" },
                  { title: t("systemStatistics.diskLoad"), series: "disk.degradation", scope: root, format: "number" },
                ]}
              />
            </div>
          ))
        ))}

      {tab === "network" && (
        <>
          <Charts
            range={range}
            specs={[
              { title: t("systemStatistics.bandwidth"), series: "net.bytes_per_sec", format: "rate" },
              { title: t("systemStatistics.downloadSpeed"), series: "downloads.bytes_per_sec", format: "rate" },
              { title: t("systemStatistics.internetLatency"), series: "net.latency_ms", format: "ms" },
            ]}
          />
          {scopesOf("net.latency_ms").map((provider) => (
            <div key={provider} className="flex flex-col gap-2">
              <h2 className="font-mono text-sm">{provider}</h2>
              <Charts
                range={range}
                specs={[
                  { title: t("systemStatistics.providerLatency"), series: "net.latency_ms", scope: provider, format: "ms" },
                  { title: t("systemStatistics.providerThroughput"), series: "net.bytes_per_sec", scope: provider, format: "rate" },
                ]}
              />
            </div>
          ))}
        </>
      )}
    </div>
  );
}
