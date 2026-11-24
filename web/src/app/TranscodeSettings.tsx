import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useEffect, useState } from "react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t, tOr, type MessageKey } from "@/lib/i18n";
import { Button } from "@/ui/Button";
import { inputStyle, labelStyle } from "@/ui/form";

import { SafeModeBanner } from "./SafeModeBanner";
import { Card, Toggle } from "./SettingsCard";
import { formatSize } from "./work/workFormat";

type Global = components["schemas"]["TranscodeGlobalSettings"];
type Library = components["schemas"]["TranscodeLibrary"];
type LaneLimits = components["schemas"]["TranscodeLaneLimits"];
type Counts = components["schemas"]["TranscodeCounts"];
type Savings = components["schemas"]["TranscodeSavings"];

const ORDERS = ["found", "alphabetical", "largest", "smallest", "oldest", "newest"] as const;
const LANES = ["cpu", "gpu", "audio"] as const;
const CODECS = ["h264", "hevc", "av1", "vp9"] as const;
const ENGINES = ["software", "qsv", "vaapi", "nvenc"] as const;
const STATES = [
  ["needed", "transcode.needed"],
  ["running", "transcode.running"],
  ["done", "transcode.done"],
  ["not_needed", "transcode.notNeeded"],
  ["failed", "transcode.failed"],
  ["blocked", "transcode.blocked"],
] as const satisfies readonly (readonly [keyof Counts, MessageKey])[];

/** Settings > Transcode: the switch and how it is doing, the libraries, the lane limits, the originals kept and the hardware. */
export function TranscodeSettings() {
  const queryClient = useQueryClient();
  const settings = useQuery({ queryKey: ["transcode-settings"], queryFn: () => ok(api.GET("/settings/transcode")) });
  const summary = useQuery({ queryKey: ["transcode-summary"], queryFn: () => ok(api.GET("/transcode/summary")), refetchInterval: 15_000 });
  const saveGlobal = useMutation({
    meta: { handlesErrors: true },
    mutationFn: (body: Global) => ok(api.PUT("/settings/transcode", { body })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["transcode-settings"] });
      void queryClient.invalidateQueries({ queryKey: ["transcode-summary"] });
    },
  });
  const global = settings.data?.global;
  const sum = summary.data;
  return (
    <div className="flex flex-col gap-6">
      <Card
        title={t("transcode.title")}
        help={t("transcode.help")}
        toggle={<Toggle label={t("transcode.switch")} checked={global?.enabled ?? false} disabled={!global || saveGlobal.isPending} onChange={(enabled) => global && saveGlobal.mutate({ ...global, enabled })} />}
      >
        <SafeModeBanner />
        <Banners summary={sum} />
      </Card>
      {sum && <Pipeline summary={sum} />}
      <p className="text-xs">
        <Link to="/settings/transcode/files" className="text-accent hover:underline">
          {t("transcode.filesLink")}
        </Link>
      </p>
      {sum && <Statistics summary={sum} />}
      <LibrariesCard libraries={settings.data?.libraries ?? []} />
      {sum && <TargetsCard summary={sum} libraries={settings.data?.libraries ?? []} />}
      {global && <ParallelCard global={global} onSave={(next) => saveGlobal.mutate(next)} busy={saveGlobal.isPending} />}
      {global && <OriginalsCard global={global} held={sum?.originals_bytes ?? 0} count={sum?.originals_count ?? 0} nextExpiry={sum?.originals_next_expiry} onSave={(next) => saveGlobal.mutate(next)} busy={saveGlobal.isPending} />}
      <HardwareCard />
    </div>
  );
}

function Banners({ summary }: { summary: components["schemas"]["TranscodeSummary"] | undefined }) {
  const queryClient = useQueryClient();
  const resume = useMutation({
    mutationFn: (kind: "video" | "audio" | "comic") => ok(api.POST("/transcode/health/{kind}/resume", { params: { path: { kind } } })),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["transcode-summary"] }),
  });
  if (!summary) return null;
  const warnings = summary.libraries.flatMap((library) => (library.warnings ?? []).map((warning) => ({ library, warning })));
  return (
    <div className="flex flex-col gap-2">
      {warnings.map(({ library, warning }) => (
        <p key={`${library.category_id}-${warning.code}`} role="alert" className="rounded-md border border-danger bg-danger/10 p-2 text-sm text-danger">
          {library.name}: {tOr(`transcode.warning.${warning.code}`, warning.code).replace("{value}", warning.value ?? "")}
        </p>
      ))}
      {summary.disk_full_roots > 0 && (
        <p role="status" className="rounded-md border border-warn p-2 text-sm">
          {t("transcode.diskFull", { count: summary.disk_full_roots })}
        </p>
      )}
      {summary.health
        .filter((health) => health.mode !== "ok")
        .map((health) => (
          <p key={health.kind} role="status" className="flex flex-wrap items-center gap-3 rounded-md border border-warn p-2 text-sm">
            <span>
              {t("transcode.paused", {
                kind: health.kind === "video" ? "Video" : health.kind === "comic" ? "Comic" : "Audio",
                time: health.backoff_until ? new Date(health.backoff_until).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "-",
                count: health.consecutive_failures,
                error: health.last_error ?? "",
              })}
            </span>
            <Button busy={resume.isPending} onClick={() => resume.mutate(health.kind)}>
              {t("transcode.tryNow")}
            </Button>
          </p>
        ))}
    </div>
  );
}

function Pipeline({ summary }: { summary: components["schemas"]["TranscodeSummary"] }) {
  const queryClient = useQueryClient();
  const [asking, setAsking] = useState<number | null>(null);
  const [count, setCount] = useState<number | null>(null);
  const preview = useMutation({
    mutationFn: (categoryId: number) => ok(api.POST("/transcode/items/retry", { body: { category_id: categoryId || undefined, preview: true } })),
    onSuccess: (result, categoryId) => {
      setCount(result.count);
      setAsking(categoryId);
    },
  });
  const retry = useMutation({
    mutationFn: (categoryId: number) => ok(api.POST("/transcode/items/retry", { body: { category_id: categoryId || undefined } })),
    onSuccess: () => {
      setAsking(null);
      void queryClient.invalidateQueries({ queryKey: ["transcode-summary"] });
    },
  });
  const rows = [{ key: 0, name: t("transcode.allLibraries"), counts: summary.counts }, ...summary.libraries.map((library) => ({ key: library.category_id, name: library.name, counts: library.counts }))];
  return (
    <Card title={t("transcode.pipeline")} help={t("transcode.pipelineHelp")}>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-xs text-ink-muted">
              <th className="py-1 pr-3 font-normal" />
              {STATES.map(([, label]) => (
                <th key={label} className="px-2 py-1 text-right font-normal">
                  {t(label)}
                </th>
              ))}
              <th />
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.key} className="border-t border-border-subtle">
                <td className="py-1.5 pr-3">{row.name}</td>
                {STATES.map(([state]) => (
                  <td key={state} className="px-2 py-1.5 text-right tabular-nums">
                    <Link to="/settings/transcode/files" search={{ state, ...(row.key ? { library: String(row.key) } : {}) }} className="hover:underline">
                      {row.counts[state].toLocaleString()}
                    </Link>
                  </td>
                ))}
                <td className="py-1.5 pl-2 text-right">
                  {row.counts.failed + row.counts.blocked > 0 &&
                    (asking === row.key ? (
                      <span className="flex items-center justify-end gap-2 text-xs">
                        {count === 0 ? t("transcode.retryNone") : t("transcode.retryConfirm", { count: count ?? 0 })}
                        <Button variant="primary" busy={retry.isPending} disabled={count === 0} onClick={() => retry.mutate(row.key)}>
                          {t("transcode.retryFailed")}
                        </Button>
                        <Button onClick={() => setAsking(null)}>{t("transcode.cancel")}</Button>
                      </span>
                    ) : (
                      <Button busy={preview.isPending} onClick={() => preview.mutate(row.key)}>
                        {t("transcode.retryFailed")}
                      </Button>
                    ))}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}

function SavingsCells({ savings }: { savings: Savings }) {
  return (
    <>
      <td className="px-2 py-1.5 text-right tabular-nums">{savings.files.toLocaleString()}</td>
      <td className="px-2 py-1.5 text-right tabular-nums">{formatSize(savings.original_bytes)}</td>
      <td className="px-2 py-1.5 text-right tabular-nums">{formatSize(savings.result_bytes)}</td>
      <td className="px-2 py-1.5 text-right tabular-nums">
        {formatSize(savings.saved_bytes)} ({savings.saved_pct} %)
      </td>
    </>
  );
}

function Statistics({ summary }: { summary: components["schemas"]["TranscodeSummary"] }) {
  return (
    <Card title={t("transcode.statistics")} help={t("transcode.statisticsHelp")}>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-xs text-ink-muted">
              <th className="py-1 pr-3 font-normal" />
              {(["transcode.files", "transcode.original", "transcode.result", "transcode.saved"] as const).map((key) => (
                <th key={key} className="px-2 py-1 text-right font-normal">
                  {t(key)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            <tr className="border-t border-border-subtle font-medium">
              <td className="py-1.5 pr-3">{t("transcode.allLibraries")}</td>
              <SavingsCells savings={summary.totals} />
            </tr>
            {summary.libraries.map((library) => (
              <tr key={library.category_id} className="border-t border-border-subtle">
                <td className="py-1.5 pr-3">{library.name}</td>
                <SavingsCells savings={library.savings} />
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}

function LibrariesCard({ libraries }: { libraries: Library[] }) {
  return (
    <Card title={t("transcode.libraries")} help={t("transcode.librariesHelp")}>
      <div className="flex flex-col divide-y divide-border-subtle">
        {libraries.map((library) => (
          <LibraryRow key={library.category_id} library={library} />
        ))}
      </div>
    </Card>
  );
}

function LibraryRow({ library }: { library: Library }) {
  const queryClient = useQueryClient();
  const [priority, setPriority] = useState(String(library.priority));
  useEffect(() => setPriority(String(library.priority)), [library.priority]);
  const save = useMutation({
    meta: { handlesErrors: true },
    mutationFn: (row: components["schemas"]["TranscodeLibraryRow"]) =>
      ok(api.PUT("/settings/transcode/libraries/{category_id}", { params: { path: { category_id: library.category_id } }, body: row })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["transcode-settings"] });
      void queryClient.invalidateQueries({ queryKey: ["transcode-summary"] });
    },
  });
  const row = { enabled: library.enabled, priority: library.priority, order: library.order };
  return (
    <div className={`flex flex-wrap items-end gap-4 py-2 ${library.supported ? "" : "opacity-60"}`}>
      <div className="flex min-w-40 flex-1 flex-col">
        <span className="text-sm font-medium">{library.name}</span>
        <span className="text-xs text-ink-muted">
          {library.supported && library.target_kind === "comic" ? (
            t("transcode.comicTarget")
          ) : library.supported && library.target_id != null ? (
            <>
              {t("transcode.target")}:{" "}
              <Link to="/settings/transcode/targets/$targetId" params={{ targetId: String(library.target_id) }} className="text-accent hover:underline">
                {library.target_name ?? ""}
              </Link>
            </>
          ) : (
            t("transcode.unsupported")
          )}
        </span>
      </div>
      <Toggle label={t("transcode.libraryOn")} checked={library.enabled} disabled={!library.supported || save.isPending} onChange={(enabled) => save.mutate({ ...row, enabled })} />
      <label className={labelStyle}>
        {t("transcode.priority")}
        <input
          type="number"
          min={-100}
          max={100}
          className={`${inputStyle} w-20`}
          disabled={!library.supported}
          value={priority}
          onChange={(event) => setPriority(event.target.value)}
          onBlur={() => priority !== String(library.priority) && Number.isFinite(Number(priority)) && save.mutate({ ...row, priority: Number(priority) })}
        />
      </label>
      <label className={labelStyle}>
        {t("transcode.order")}
        <select
          className={inputStyle}
          disabled={!library.supported}
          value={library.order}
          onChange={(event) => save.mutate({ ...row, order: event.target.value as (typeof ORDERS)[number] })}
        >
          {ORDERS.map((order) => (
            <option key={order} value={order}>
              {t(`transcode.order.${order}`)}
            </option>
          ))}
        </select>
      </label>
    </div>
  );
}

function LaneInputs({ label, value, onChange }: { label: string; value: LaneLimits; onChange: (next: LaneLimits) => void }) {
  return (
    <div className="flex flex-wrap items-end gap-4">
      <span className="w-48 text-sm">{label}</span>
      {LANES.map((lane) => (
        <label key={lane} className={labelStyle}>
          {t(`transcode.lane.${lane}`)}
          <input
            type="number"
            min={0}
            max={16}
            className={`${inputStyle} w-20`}
            value={value[lane]}
            onChange={(event) => onChange({ ...value, [lane]: Math.max(0, Math.min(16, Number(event.target.value) || 0)) })}
          />
        </label>
      ))}
    </div>
  );
}

function ParallelCard({ global, onSave, busy }: { global: Global; onSave: (next: Global) => void; busy: boolean }) {
  const [parallel, setParallel] = useState(global.parallel);
  const [playing, setPlaying] = useState(global.playback_parallel);
  useEffect(() => {
    setParallel(global.parallel);
    setPlaying(global.playback_parallel);
  }, [global.parallel, global.playback_parallel]);
  return (
    <Card title={t("transcode.parallel")} help={t("transcode.parallelHelp")}>
      <LaneInputs label={t("transcode.normally")} value={parallel} onChange={setParallel} />
      <LaneInputs label={t("transcode.whileWatching")} value={playing} onChange={setPlaying} />
      <div>
        <Button variant="primary" busy={busy} onClick={() => onSave({ ...global, parallel, playback_parallel: playing })}>
          {t("transcode.save")}
        </Button>
      </div>
    </Card>
  );
}

function TargetsCard({ summary, libraries }: { summary: components["schemas"]["TranscodeSummary"]; libraries: Library[] }) {
  const targets = new Map<number, { name: string; warnings: number }>();
  for (const library of summary.libraries) {
    if (library.target_id == null) continue;
    const name = libraries.find((row) => row.target_id === library.target_id)?.target_name ?? library.name;
    const known = targets.get(library.target_id) ?? { name, warnings: 0 };
    known.warnings = Math.max(known.warnings, (library.warnings ?? []).length);
    targets.set(library.target_id, known);
  }
  if (targets.size === 0) return null;
  return (
    <Card title={t("transcode.targets")} help={t("transcode.targetsHelp")}>
      <div className="flex flex-col divide-y divide-border-subtle text-sm">
        {[...targets].map(([id, one]) => (
          <div key={id} className="flex flex-wrap items-center justify-between gap-3 py-1.5">
            <Link to="/settings/transcode/targets/$targetId" params={{ targetId: String(id) }} className="text-accent hover:underline">
              {one.name}
            </Link>
            <span className={one.warnings > 0 ? "text-danger" : "text-ink-muted"}>{one.warnings > 0 ? t("transcode.targetWarnings", { count: one.warnings }) : t("transcode.targetDefault")}</span>
          </div>
        ))}
      </div>
    </Card>
  );
}

function OriginalsCard({ global, held, count, nextExpiry, onSave, busy }: { global: Global; held: number; count: number; nextExpiry: string | undefined; onSave: (next: Global) => void; busy: boolean }) {
  const [days, setDays] = useState(String(global.originals_retain_days));
  const [cap, setCap] = useState(String(global.originals_cap_gb));
  useEffect(() => {
    setDays(String(global.originals_retain_days));
    setCap(String(global.originals_cap_gb));
  }, [global.originals_retain_days, global.originals_cap_gb]);
  return (
    <Card title={t("transcode.originals")} help={[t("transcode.originalsHelp", { size: formatSize(held) }), nextExpiry ? t("transcode.originalsNext", { count, date: new Date(nextExpiry).toLocaleDateString() }) : ""].filter(Boolean).join(" ")}>
      <div className="flex flex-wrap items-end gap-4">
        <label className={labelStyle}>
          {t("transcode.retainDays")}
          <input type="number" min={0} max={3650} className={`${inputStyle} w-28`} value={days} onChange={(event) => setDays(event.target.value)} />
        </label>
        <label className={labelStyle}>
          {t("transcode.capGb")}
          <input type="number" min={0} className={`${inputStyle} w-28`} value={cap} onChange={(event) => setCap(event.target.value)} />
        </label>
        <Button
          variant="primary"
          busy={busy}
          onClick={() => onSave({ ...global, originals_retain_days: Math.max(0, Math.round(Number(days) || 0)), originals_cap_gb: Math.max(0, Math.round(Number(cap) || 0)) })}
        >
          {t("transcode.save")}
        </Button>
      </div>
    </Card>
  );
}

function HardwareCard() {
  const queryClient = useQueryClient();
  const hardware = useQuery({ queryKey: ["transcode-hardware"], queryFn: () => ok(api.GET("/transcode/hardware")) });
  const detect = useMutation({
    mutationFn: () => ok(api.POST("/transcode/hardware/detect")),
    onSuccess: (data) => queryClient.setQueryData(["transcode-hardware"], data),
  });
  const data = hardware.data;
  const row = (engine: (typeof ENGINES)[number], codec: (typeof CODECS)[number]) => data?.encoders.find((one) => one.engine === engine && one.codec === codec);
  return (
    <Card title={t("transcode.hardware")} help={t("transcode.hardwareHelp")}>
      {data && !data.ffmpeg_available && <p className="text-sm text-danger">{t("transcode.noFfmpeg")}</p>}
      {data?.ffmpeg_available && <p className="text-xs text-ink-muted">{t("transcode.ffmpeg", { version: data.ffmpeg_version })}</p>}
      {data?.capabilities
        .filter((capability) => capability.accel !== "software")
        .map((capability) => (
          <p key={capability.accel} className="text-xs text-ink-muted">
            {capability.accel.toUpperCase()}: {capability.name ?? ""} {capability.device ?? ""} {capability.working ? "" : `(${capability.detail ?? t("transcode.unavailable")})`}
          </p>
        ))}
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-xs text-ink-muted">
              <th className="py-1 pr-3 font-normal">{t("transcode.engine")}</th>
              {CODECS.map((codec) => (
                <th key={codec} className="px-2 py-1 font-normal">
                  {codec.toUpperCase()}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {ENGINES.map((engine) => (
              <tr key={engine} className="border-t border-border-subtle">
                <td className="py-1.5 pr-3">{engine}</td>
                {CODECS.map((codec) => {
                  const support = row(engine, codec);
                  return (
                    <td key={codec} className="px-2 py-1.5 align-top text-xs">
                      {support?.working ? (
                        <span className="text-ok">
                          ✓ {support.ten_bit ? t("transcode.tenBit") : ""} {support.lookahead ? t("transcode.lookahead") : ""}
                        </span>
                      ) : (
                        <span className="text-ink-muted">{support?.detail ?? t("transcode.unavailable")}</span>
                      )}
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div>
        <Button busy={detect.isPending} onClick={() => detect.mutate()}>
          {t("transcode.detect")}
        </Button>
      </div>
    </Card>
  );
}
