import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";

import { api, ok } from "@/api/client";
import { isTierVisible, useTier } from "@/lib/useTier";
import type { components } from "@/api/schema";

import { Button } from "@/ui/Button";

import { Card, Field, SaveBar } from "./SettingsCard";

type CacheReport = components["schemas"]["CacheReport"];

// A gigabyte as formatBytes counts one, so the 10 GiB default reads as 10.
const GIB = 1024 ** 3;
type Budget = components["schemas"]["CacheReport"] & { min_free_percent: number };

/** The Cache settings page: CacheSettings' own section, given its own page
 *  2026-09-11 (split out of one long Settings scroll). Expert tier (the
 *  registry's own cache.* keys, todo 4.14) -- unlike Network/Priority,
 *  which sit inside a mixed-tier General page and simply disappear from
 *  it, this page's entire content is expert-only, so a caller below that
 *  tier who lands here directly (Sidebar already hides the nav entry) sees
 *  an explanation rather than a blank page under a lone heading. */
export function CacheSettingsPage() {
  const tier = useTier();
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <header className="flex flex-col gap-1">
          <h1 className="text-base font-semibold">Cache</h1>
        </header>
        {isTierVisible(tier, "expert") ? (
          <CacheSettings />
        ) : (
          <p className="text-sm text-ink-muted">
            Cache sizing is an expert-tier setting. Raise your own tier on the User settings page
            to see it.
          </p>
        )}
      </div>
    </div>
  );
}

/** todo 4.19, ADR-60: the cache is bounded by size and age, and reports its
 *  own size -- current size per namespace with when it was measured, both
 *  budgets, and a manual clear that is always safe (Invariant 5). */
export function CacheSettings() {
  const queryClient = useQueryClient();
  const report = useQuery({
    queryKey: ["cache"],
    queryFn: () => ok(api.GET("/cache")),
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["cache"] });

  const update = useMutation({
    mutationFn: async (budget: Budget) => {
      await ok(api.PATCH("/cache", { body: budget }));
    },
    onSuccess: invalidate,
  });

  const clear = useMutation({
    mutationFn: async () => {
      await ok(api.POST("/cache/clear", {}));
    },
    onSuccess: invalidate,
  });

  const shrink = useMutation({
    mutationFn: async () => {
      await ok(api.POST("/cache/shrink", {}));
    },
    onSuccess: invalidate,
  });

  if (report.error !== null || !report.data) return null;

  return (
    <section className="flex flex-col gap-4">
      <p className="max-w-2xl text-sm text-ink-muted">
        Provider responses and resized artwork. Never the only copy of anything: a miss just costs a re-fetch, so clearing it
        is always safe.
      </p>

      <Card title="Usage" help="How much of its budget each part of the cache uses.">
        <NamespaceBar
          label="Images"
          bytes={report.data.images_bytes}
          budgetBytes={(report.data.max_bytes * report.data.images_share_percent) / 100}
        />
        <NamespaceBar
          label="Metadata"
          bytes={report.data.metadata_bytes}
          budgetBytes={(report.data.max_bytes * report.data.metadata_share_percent) / 100}
        />
        <p className="text-xs text-ink-muted">
          Hit ratio since the start: images {hitRatio(report.data.images_hits, report.data.images_misses)}, provider responses{" "}
          {hitRatio(report.data.metadata_hits, report.data.metadata_misses)}.
        </p>
        <p className="text-xs text-ink-muted">
          {report.data.measured_at
            ? `Measured ${new Date(report.data.measured_at).toLocaleString()}.`
            : "Not measured yet: the cache maintenance task has not run."}
        </p>
        {report.data.disk_total_bytes != null && report.data.disk_free_bytes != null && (
          <p className="text-xs text-ink-muted">
            The disk under the cache: {formatBytes(report.data.disk_free_bytes)} free of {formatBytes(report.data.disk_total_bytes)}
            {report.data.max_bytes > report.data.disk_total_bytes && (
              <span className="text-warn">; the size limit is larger than the whole disk</span>
            )}
            .
          </p>
        )}
        {report.data.shrunk_to_bytes != null && (
          <p className="text-xs text-warn">
            The last sweep held the cache to {formatBytes(report.data.shrunk_to_bytes)} to keep {report.data.min_free_percent}% of the
            disk free.
          </p>
        )}
      </Card>

      <DatabaseMemoryCard />

      <BudgetForm report={report.data} onSave={(budget) => update.mutate(budget)} busy={update.isPending} />
      {update.isError && <p className="text-sm text-danger">That budget was refused.</p>}

      <MemoryCopiesCard />

      <Card
        title="Shrink or clear the cache"
        help="Shrink drops what has not been used for seven days. Clear throws everything away; either is fetched again as it is needed."
      >
        <div className="flex flex-wrap items-center gap-3">
          <Button variant="secondary" onClick={() => shrink.mutate()} disabled={shrink.isPending}>
            {shrink.isPending ? "Shrinking…" : "Shrink cache"}
          </Button>
        </div>
        {shrink.isError && <p className="text-xs text-danger">The cache could not be shrunk.</p>}
        <ClearButton onClear={() => clear.mutate()} busy={clear.isPending} />
        {clear.isError && <p className="text-xs text-danger">The cache could not be cleared.</p>}
      </Card>
    </section>
  );
}

type MemoryCopySettings = components["schemas"]["MemoryCopySettings"];

/** The cache and the databases kept in RAM and copied back to disk: both off by default, each turned on only after
 *  the person ticks that they understand the risk and the container has the memory. */
function MemoryCopiesCard() {
  const queryClient = useQueryClient();
  const status = useQuery({
    queryKey: ["cache-memory-copies"],
    queryFn: () => ok(api.GET("/cache/memory-copies")),
    refetchInterval: 15_000,
  });
  const [draft, setDraft] = useState<MemoryCopySettings | null>(null);
  const save = useMutation({
    mutationFn: async (body: MemoryCopySettings) => {
      await ok(api.PUT("/cache/memory-copies", { body }));
    },
    onSuccess: () => {
      setDraft(null);
      return queryClient.invalidateQueries({ queryKey: ["cache-memory-copies"] });
    },
  });
  if (status.error !== null || !status.data) return null;
  const data = status.data;
  const current: MemoryCopySettings = draft ?? {
    cache_enabled: data.cache_enabled,
    database_enabled: data.database_enabled,
    cache_sync_minutes: data.cache_sync_minutes,
    database_sync_minutes: data.database_sync_minutes,
    acknowledged: false,
  };
  const turnsOn =
    (current.cache_enabled && !data.cache_enabled) || (current.database_enabled && !data.database_enabled);
  const dirty =
    current.cache_enabled !== data.cache_enabled ||
    current.database_enabled !== data.database_enabled ||
    current.cache_sync_minutes !== data.cache_sync_minutes ||
    current.database_sync_minutes !== data.database_sync_minutes;
  const need = (current.cache_enabled ? data.cache_bytes_needed : 0) + (current.database_enabled ? data.database_bytes_needed : 0);
  const onDisk = (current.cache_enabled ? data.cache_disk_bytes : 0) + (current.database_enabled ? data.database_disk_bytes : 0);
  const shmTooSmall = data.tmpfs_total_bytes < onDisk * 1.2;
  const short = shmTooSmall || need > data.available_bytes || need > data.tmpfs_free_bytes;
  const change = (patch: Partial<MemoryCopySettings>) => setDraft({ ...current, ...patch });

  return (
    <Card
      title="Keep the cache and databases in RAM"
      tone="warn"
      help="Runs them from memory and copies them back to disk on an interval. Faster, but it takes a lot of additional RAM, and what changed since the last copy is lost if Solarr or the machine dies."
    >
      <dl className="grid max-w-xl grid-cols-[1fr_auto] gap-x-6 gap-y-0.5 text-xs">
        {(
          [
            [`Shared memory (${data.tmpfs_dir}), size`, formatBytes(data.tmpfs_total_bytes)],
            ["  free", formatBytes(data.tmpfs_free_bytes)],
            ["Cache on disk now", formatBytes(data.cache_disk_bytes)],
            ["Databases on disk now", formatBytes(data.database_disk_bytes)],
            ["Shared memory needed to turn the cache on (+20%)", formatBytes(data.cache_disk_bytes * 1.2)],
            ["Shared memory needed to turn the databases on (+20%)", formatBytes(data.database_disk_bytes * 1.2)],
            ["Shared memory needed to turn both on (+20%)", formatBytes((data.cache_disk_bytes + data.database_disk_bytes) * 1.2)],
          ] as const
        ).map(([label, value]) => (
          <div key={label} className="contents">
            <dt className="whitespace-pre text-ink-muted">{label}</dt>
            <dd className="text-right tabular-nums">{value}</dd>
          </div>
        ))}
      </dl>
      {data.tmpfs_total_bytes < (data.cache_disk_bytes + data.database_disk_bytes) * 1.2 && (
        <p className="text-xs text-warn">
          The shared memory is smaller than both need. Raise shm_size for the container (docker run --shm-size, or shm_size in compose) and restart it; an option is turned on only when
          the shared memory is at least 20% larger than what it would hold.
        </p>
      )}
      <p className="text-xs text-ink-muted">
        {data.limit_bytes > 0
          ? `This container may use ${formatBytes(data.limit_bytes)} and has ${formatBytes(data.available_bytes)} left`
          : `The container has no memory limit; the machine has ${formatBytes(data.available_bytes)} left`}
        ; {data.tmpfs_dir} has room for {formatBytes(data.tmpfs_free_bytes)} (raise shm_size to give it more).
      </p>
      <MemoryCopyRow
        label="Cache (pictures and provider responses)"
        needed={data.cache_bytes_needed}
        enabled={current.cache_enabled}
        running={data.cache_running}
        minutes={current.cache_sync_minutes}
        lastSync={data.last_cache_sync}
        disabled={save.isPending}
        onEnabled={(cache_enabled) => change({ cache_enabled })}
        onMinutes={(cache_sync_minutes) => change({ cache_sync_minutes })}
      />
      <MemoryCopyRow
        label="Databases"
        needed={data.database_bytes_needed}
        enabled={current.database_enabled}
        running={data.database_running}
        minutes={current.database_sync_minutes}
        lastSync={data.last_database_sync}
        disabled={save.isPending}
        onEnabled={(database_enabled) => change({ database_enabled })}
        onMinutes={(database_sync_minutes) => change({ database_sync_minutes })}
      />
      {current.database_enabled && (
        <p className="rounded-md border border-danger p-3 text-sm font-semibold text-danger">
          DATA LOSS RISK: the databases hold everything Solarr knows. A crash, a power cut or an out-of-memory kill loses all changes since the last copy to disk.
        </p>
      )}
      {(current.cache_enabled || current.database_enabled) && (
        <p className={`text-xs ${short ? "text-danger" : "text-ink-muted"}`}>
          Needs {formatBytes(need)} of RAM (their size plus 10%).
          {shmTooSmall ? " The shared memory is not 20% larger than that, so it cannot be turned on." : short ? " That is more than is free, so it cannot be turned on." : ""}
        </p>
      )}
      {turnsOn && (
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={current.acknowledged} onChange={(event) => change({ acknowledged: event.target.checked })} />
          I understand the risk
        </label>
      )}
      <div className="flex items-center gap-3">
        <Button variant="primary" onClick={() => save.mutate(current)} disabled={save.isPending || !dirty || (turnsOn && (!current.acknowledged || short))}>
          {save.isPending ? "Saving…" : "Save"}
        </Button>
        {data.restart_required && <span className="text-xs text-warn">Saved; restart Solarr to apply it.</span>}
      </div>
      {save.isError && <p className="text-xs text-danger">Refused: the container does not have the memory, or the risk was not confirmed.</p>}
      {data.notice && <p className="text-xs text-warn">{data.notice}</p>}
    </Card>
  );
}

function MemoryCopyRow({
  label,
  needed,
  enabled,
  running,
  minutes,
  lastSync,
  disabled,
  onEnabled,
  onMinutes,
}: {
  label: string;
  needed: number;
  enabled: boolean;
  running: boolean;
  minutes: number;
  lastSync?: string;
  disabled: boolean;
  onEnabled: (next: boolean) => void;
  onMinutes: (next: number) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-4 text-sm">
      <label className="flex min-w-72 items-center gap-2">
        <input type="checkbox" checked={enabled} disabled={disabled} onChange={(event) => onEnabled(event.target.checked)} />
        {label}
      </label>
      <span className="text-xs text-ink-muted">would take {formatBytes(needed)}</span>
      <label className="flex items-center gap-1 text-xs text-ink-muted">
        copy to disk every
        <input
          type="number"
          min={1}
          value={minutes}
          disabled={disabled}
          onChange={(event) => onMinutes(Math.max(1, Number(event.target.value) || 1))}
          className={`${inputStyle} w-16`}
        />
        min
      </label>
      <span className="text-xs text-ink-muted">
        {running ? `running from RAM${lastSync ? `, last copy ${new Date(lastSync).toLocaleTimeString()}` : ""}` : "from disk"}
      </span>
    </div>
  );
}

/** The SQLite page cache: the size setting (config.yml, applied at the next start), each database's file size and how
 *  full and how effective its cache is. SQLite counts the hits itself, so showing them costs the databases nothing. */
function DatabaseMemoryCard() {
  const queryClient = useQueryClient();
  const memory = useQuery({
    queryKey: ["cache-memory"],
    queryFn: () => ok(api.GET("/cache/memory")),
    refetchInterval: 15_000,
  });
  const [mib, setMib] = useState<number | null>(null);
  const save = useMutation({
    mutationFn: async (kilobytes: number) => {
      await ok(api.PUT("/cache/memory", { body: { cache_size_kb: kilobytes } }));
    },
    onSuccess: () => {
      setMib(null);
      return queryClient.invalidateQueries({ queryKey: ["cache-memory"] });
    },
  });
  if (memory.error !== null || !memory.data) return null;
  const data = memory.data;
  const shown = mib ?? Math.round(data.configured_kb / 1024);
  const dirty = shown * 1024 !== data.configured_kb;
  const submit = () => save.mutate(shown * 1024);

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      <Card
        title="Database memory"
        help="One ceiling for the page caches SQLite keeps in memory, all databases and connections together. The library takes most of it. A bigger ceiling means fewer reads from disk; it is a limit, not memory held from the start."
      >
        <div className="flex flex-wrap items-end gap-4">
          <Field label="Total database cache ceiling (MB)" help={`Default ${Math.round(data.default_kb / 1024)} MB.`} className="w-56">
            <input
              type="number"
              min={128}
              max={16384}
              value={shown}
              disabled={save.isPending}
              onChange={(event) => setMib(Number(event.target.value))}
              className={inputStyle}
            />
          </Field>
          <button
            type="button"
            onClick={() => setMib(Math.round(data.default_kb / 1024))}
            disabled={save.isPending || shown * 1024 === data.default_kb}
            className="mb-5 rounded-md border border-border-subtle px-2 py-1.5 text-xs text-ink-muted hover:text-ink disabled:opacity-50
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            Set defaults
          </button>
          <div className="mb-5">
            <Button variant="primary" type="submit" disabled={save.isPending || !dirty}>
              {save.isPending ? "Saving…" : "Save"}
            </Button>
          </div>
        </div>
        {data.restart_required && (
          <p className="text-xs text-warn">
            Saved as {Math.round(data.configured_kb / 1024)} MB; the databases run with {Math.round(data.running_kb / 1024)} MB until Solarr restarts.
          </p>
        )}
        {save.isError && <p className="text-xs text-danger">That size was refused.</p>}
        <ul className="flex flex-col gap-2">
          {data.databases.map((row) => (
            <li key={row.name} className="flex flex-col gap-1">
              <div className="flex items-center justify-between text-xs">
                <span className="font-medium capitalize">{row.name}</span>
                <span className="text-ink-muted">
                  file {formatBytes(row.file_bytes)}
                  {row.wal_bytes > 0 ? ` + log ${formatBytes(row.wal_bytes)}` : ""}
                </span>
              </div>
              <div className="h-2 overflow-hidden rounded-full bg-canvas">
                <div
                  className="h-full rounded-full bg-accent"
                  style={{ width: `${row.limit_bytes > 0 ? Math.min(100, Math.round((row.used_bytes / row.limit_bytes) * 100)) : 0}%` }}
                />
              </div>
              <span className="text-xs text-ink-muted">
                {row.connections === 0
                  ? "cache figures not available"
                  : `cache ${formatBytes(row.used_bytes)} of ${formatBytes(row.limit_bytes)} (${row.connections} connections) · hit ratio ${hitRatio(row.hits, row.misses)}`}
              </span>
            </li>
          ))}
        </ul>
        <p className="text-xs text-ink-muted">Hit ratios count since Solarr started.</p>
      </Card>
    </form>
  );
}

function hitRatio(hits: number | undefined, misses: number | undefined): string {
  const total = (hits ?? 0) + (misses ?? 0);
  if (total === 0) return "no lookups yet";
  return `${(((hits ?? 0) / total) * 100).toFixed(1)}%`;
}

function NamespaceBar({ label, bytes, budgetBytes }: { label: string; bytes?: number; budgetBytes: number }) {
  const used = bytes ?? 0;
  const pct = budgetBytes > 0 ? Math.min(100, Math.round((used / budgetBytes) * 100)) : 0;
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center justify-between text-xs">
        <span className="font-medium">{label}</span>
        <span className="text-ink-muted">
          {bytes === undefined ? "not measured" : `${formatBytes(used)} / ${formatBytes(budgetBytes)}`}
        </span>
      </div>
      <div className="h-2.5 overflow-hidden rounded-full bg-canvas">
        <div className="h-full rounded-full bg-accent" style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

function BudgetForm({
  report,
  onSave,
  busy,
}: {
  report: CacheReport;
  onSave: (budget: Budget) => void;
  busy: boolean;
}) {
  const [maxGB, setMaxGB] = useState(Math.round(report.max_bytes / GIB));
  const [maxAgeDays, setMaxAgeDays] = useState(report.max_age_days);
  const [imagesShare, setImagesShare] = useState(report.images_share_percent);
  const [minFree, setMinFree] = useState(report.min_free_percent);

  const dirty =
    maxGB !== Math.round(report.max_bytes / GIB) ||
    maxAgeDays !== report.max_age_days ||
    imagesShare !== report.images_share_percent ||
    minFree !== report.min_free_percent;

  const submit = () =>
    onSave({
      max_bytes: maxGB * GIB,
      max_age_days: maxAgeDays,
      images_share_percent: imagesShare,
      metadata_share_percent: 100 - imagesShare,
      min_free_percent: minFree,
    });
  const number = (value: number, set: (next: number) => void, min: number, max?: number) => (
    <input
      type="number"
      min={min}
      max={max}
      value={value}
      disabled={busy}
      onChange={(event) => set(Number(event.target.value))}
      className={inputStyle}
    />
  );

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      <Card
        title="Limits"
        help="The cache is trimmed when it passes its size, and anything older than the age is dropped. It also shrinks, below its size, when its disk runs short of the free share."
      >
        <div className="flex flex-wrap items-start gap-4">
          <Field label="Max size (GB)" className="w-36">
            {number(maxGB, setMaxGB, 1)}
          </Field>
          <Field label="Max age (days)" className="w-36">
            {number(maxAgeDays, setMaxAgeDays, 1)}
          </Field>
          <Field label="Images share (%)" help={`Metadata gets ${100 - imagesShare}%.`} className="w-36">
            {number(imagesShare, setImagesShare, 0, 100)}
          </Field>
          <Field label="Keep disk free (%)" help="0 turns it off." className="w-36">
            {number(minFree, setMinFree, 0, 90)}
          </Field>
        </div>
      </Card>
      <SaveBar dirty={dirty} busy={busy} onSave={submit} />
    </form>
  );
}

function ClearButton({ onClear, busy }: { onClear: () => void; busy: boolean }) {
  const [confirming, setConfirming] = useState(false);

  if (!confirming) {
    return (
      <button
        type="button"
        onClick={() => setConfirming(true)}
        className="flex w-fit items-center gap-1.5 rounded-md border border-border-subtle px-2 py-1
                   text-xs text-ink-muted hover:text-danger focus-visible:outline-2
                   focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        <Trash2 aria-hidden className="size-3.5" />
        Clear cache
      </button>
    );
  }
  return (
    <span className="flex items-center gap-2 text-xs">
      <span className="text-ink-muted">Clear everything? A miss just re-fetches.</span>
      <button
        type="button"
        onClick={() => {
          setConfirming(false);
          onClear();
        }}
        disabled={busy}
        className="rounded-md border border-danger px-2 py-1 text-danger disabled:opacity-50
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        Yes, clear it
      </button>
      <button
        type="button"
        onClick={() => setConfirming(false)}
        className="rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-ink"
      >
        Cancel
      </button>
    </span>
  );
}

function formatBytes(bytes: number): string {
  if (bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const exponent = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  const value = bytes / 1024 ** exponent;
  return `${value >= 10 || exponent === 0 ? Math.round(value) : value.toFixed(1)} ${units[exponent]}`;
}

const inputStyle =
  "w-full rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
