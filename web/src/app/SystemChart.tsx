import { useQueries, useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { api, ok } from "@/api/client";
import { t } from "@/lib/i18n";

import { formatBytes } from "./Queue";

export type Format = "number" | "bytes" | "rate" | "ms" | "percent";
export const RANGES = ["hour", "6hours", "day", "week", "month"] as const;
export type RangeKey = (typeof RANGES)[number];

const SPAN_SECONDS: Record<RangeKey, number> = { hour: 3600, "6hours": 21600, day: 86400, week: 604800, month: 2592000 };

export function formatValue(value: number, format: Format): string {
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

/** One line of a chart: a measured series, and of what (a library, a disk group, a client; "" for the whole). */
export interface Line {
  label: string;
  series: string;
  scope?: string;
}

/** Distinguishable by people with weak colour vision, on a light and a dark page alike (Okabe-Ito). */
const COLOURS = ["#0072B2", "#E69F00", "#009E73", "#CC79A7", "#56B4E9", "#D55E00", "#F0E442", "#999999"];
const colourOf = (index: number) => COLOURS[index % COLOURS.length]!;

const WIDTH = 640;
const HEIGHT = 120;

type Points = { at: number; value: number }[];

/** The latest and highest value of a series over a span; undefined until it has a point. */
export function useLatest(series: string, scope: string, range: RangeKey) {
  const data = useQuery({
    queryKey: ["system-metric", series, scope, range],
    queryFn: () => ok(api.GET("/statistics/system/series", { params: { query: { series, scope, range } } })),
    refetchInterval: 60_000,
  });
  const points = data.data?.points ?? [];
  if (points.length === 0) return undefined;
  return { latest: points[points.length - 1]!.value, peak: Math.max(...points.map((point) => point.value)) };
}

/** Lines over time on one scale; a legend toggles each line, and the figures at the pointer are written out. */
export function MetricChart({ title, lines, range, format }: { title: string; lines: Line[]; range: RangeKey; format: Format }) {
  const results = useQueries({
    queries: lines.map((line) => ({
      queryKey: ["system-metric", line.series, line.scope ?? "", range],
      queryFn: () => ok(api.GET("/statistics/system/series", { params: { query: { series: line.series, scope: line.scope ?? "", range } } })),
      refetchInterval: 60_000,
    })),
  });
  const [hidden, setHidden] = useState<readonly number[]>([]);
  const [hover, setHover] = useState<number | null>(null);
  const spanSeconds = SPAN_SECONDS[range];
  const loaded: Points[] = results.map((result) => result.data?.points ?? []);
  const shown = lines.map((_, index) => index).filter((index) => !hidden.includes(index) && loaded[index]!.length > 0);
  const all = shown.flatMap((index) => loaded[index]!);
  const pending = results.some((result) => result.isPending);
  const toggle = (index: number) => setHidden((current) => (current.includes(index) ? current.filter((one) => one !== index) : [...current, index]));
  const end = all.length > 0 ? Math.max(...all.map((point) => point.at)) : 0;
  const start = end - spanSeconds;
  const max = Math.max(...all.map((point) => point.value), 0) || 1;
  const x = (at: number) => ((at - start) / spanSeconds) * WIDTH;
  const y = (value: number) => HEIGHT - (value / max) * (HEIGHT - 8) - 4;
  const at = hover ?? end;
  const nearest = (points: Points) => points.reduce<{ at: number; value: number } | undefined>((best, point) => (best === undefined || Math.abs(point.at - at) < Math.abs(best.at - at) ? point : best), undefined);
  const readout = lines.map((line, index) => ({ line, index, point: nearest(loaded[index]!) }));
  const anyData = loaded.some((points) => points.length > 0);
  return (
    <section className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface p-3">
      <h2 className="text-sm font-medium">{title}</h2>
      {pending && !anyData ? (
        <p className="py-6 text-center text-xs text-ink-muted">{t("systemStatistics.loading")}</p>
      ) : !anyData ? (
        <p className="py-6 text-center text-xs text-ink-muted">{t("systemStatistics.noData")}</p>
      ) : (
        <svg
          viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
          role="img"
          aria-label={t("systemStatistics.chartLabel", { latest: readout.map((row) => `${row.line.label} ${row.point ? formatValue(row.point.value, format) : "-"}`).join(", ") })}
          className="h-28 w-full"
          preserveAspectRatio="none"
          onPointerLeave={() => setHover(null)}
          onPointerMove={(event) => {
            const box = event.currentTarget.getBoundingClientRect();
            setHover(start + ((event.clientX - box.left) / box.width) * spanSeconds);
          }}
        >
          <line x1="0" x2={WIDTH} y1={HEIGHT - 1} y2={HEIGHT - 1} className="stroke-border-subtle" strokeWidth="1" />
          {shown.map((index) => (
            <path
              key={index}
              d={loaded[index]!.map((point, step) => `${step === 0 ? "M" : "L"}${x(point.at).toFixed(1)},${y(point.value).toFixed(1)}`).join(" ")}
              fill="none"
              stroke={colourOf(index)}
              strokeWidth="1.5"
              vectorEffect="non-scaling-stroke"
            />
          ))}
          {hover !== null && <line x1={x(hover)} x2={x(hover)} y1="0" y2={HEIGHT} stroke="currentColor" opacity="0.4" vectorEffect="non-scaling-stroke" />}
        </svg>
      )}
      <div className="flex flex-wrap items-center justify-between gap-x-3 text-xs tabular-nums text-ink-muted">
        <span>{anyData ? new Date(at * 1000).toLocaleString() : ""}</span>
        {anyData && <span>{t("systemStatistics.peak", { value: formatValue(max, format) })}</span>}
      </div>
      <ul className="flex flex-wrap gap-x-3 gap-y-1 text-xs">
        {readout.map(({ line, index, point }) => (
          <li key={`${line.series}|${line.scope ?? ""}`}>
            <button
              type="button"
              aria-pressed={!hidden.includes(index)}
              title={t("systemStatistics.toggleLine")}
              onClick={() => toggle(index)}
              className={`inline-flex items-center gap-1.5 rounded px-1 py-0.5 hover:text-accent ${hidden.includes(index) ? "opacity-50 line-through" : ""}`}
            >
              <span aria-hidden className="inline-block size-2.5 rounded-sm" style={{ backgroundColor: colourOf(index) }} />
              <span>{line.label}</span>
              <span className="tabular-nums text-ink-muted">{point ? formatValue(point.value, format) : "-"}</span>
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}

const ARC = 80;

/** A half-circle showing how much of a ceiling is in use right now; the figure is written in its middle. */
export function Gauge({ label, value, max, format, note }: { label: string; value: number | undefined; max: number; format: Format; note?: string }) {
  const share = value === undefined || max <= 0 ? 0 : Math.min(1, value / max);
  const point = (fraction: number) => {
    const angle = Math.PI * (1 - fraction);
    return `${(50 + ARC / 2 * Math.cos(angle)).toFixed(2)},${(50 - ARC / 2 * Math.sin(angle)).toFixed(2)}`;
  };
  return (
    <figure className="flex flex-col items-center gap-0.5 rounded-lg border border-border-subtle bg-surface p-3">
      <svg viewBox="0 0 100 58" role="img" aria-label={`${label} ${value === undefined ? "-" : formatValue(value, format)}`} className="w-full max-w-40">
        <path d={`M${point(0)} A${ARC / 2},${ARC / 2} 0 0 1 ${point(1)}`} fill="none" className="stroke-border-subtle" strokeWidth="9" strokeLinecap="round" />
        {share > 0 && <path d={`M${point(0)} A${ARC / 2},${ARC / 2} 0 0 1 ${point(share)}`} fill="none" className="stroke-accent" strokeWidth="9" strokeLinecap="round" />}
        <text x="50" y="50" textAnchor="middle" className="fill-current text-[11px] font-semibold tabular-nums">{value === undefined ? "-" : formatValue(value, format)}</text>
      </svg>
      <figcaption className="text-center text-xs text-ink-muted">
        {label}
        {note && <span className="block text-[0.6875rem]">{note}</span>}
      </figcaption>
    </figure>
  );
}
