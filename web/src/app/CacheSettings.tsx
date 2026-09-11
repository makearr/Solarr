import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type CacheReport = components["schemas"]["CacheReport"];

/** The Cache settings page: CacheSettings' own section, given its own page
 *  2026-09-11 (split out of one long Settings scroll). */
export function CacheSettingsPage() {
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <header className="flex flex-col gap-1">
          <h1 className="text-base font-semibold">Cache</h1>
        </header>
        <CacheSettings />
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
    queryFn: async () => {
      const { data, error } = await api.GET("/cache");
      if (error) throw error;
      return data;
    },
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["cache"] });

  const update = useMutation({
    mutationFn: async (budget: {
      max_bytes: number;
      max_age_days: number;
      images_share_percent: number;
      metadata_share_percent: number;
    }) => {
      const { error } = await api.PATCH("/cache", { body: budget });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  const clear = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/cache/clear", {});
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  if (report.error !== null || !report.data) return null;

  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Cache</h2>
      <p className="text-xs text-ink-muted">
        Provider responses and resized artwork. Never the only copy of anything -- a miss just
        costs a re-fetch, so clearing it is always safe.
      </p>

      <div className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3">
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
        <p className="text-[0.6875rem] text-ink-muted">
          {report.data.measured_at
            ? `measured ${new Date(report.data.measured_at).toLocaleString()}`
            : "not measured yet -- the cache maintenance task has not run"}
        </p>

        <BudgetForm report={report.data} onSave={(budget) => update.mutate(budget)} busy={update.isPending} />
        {update.isError && <p className="text-xs text-danger">That budget was refused.</p>}

        <ClearButton onClear={() => clear.mutate()} busy={clear.isPending} />
        {clear.isError && <p className="text-xs text-danger">The cache could not be cleared.</p>}
      </div>
    </section>
  );
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
      <div className="h-1.5 overflow-hidden rounded-full bg-canvas">
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
  onSave: (budget: {
    max_bytes: number;
    max_age_days: number;
    images_share_percent: number;
    metadata_share_percent: number;
  }) => void;
  busy: boolean;
}) {
  const [maxGB, setMaxGB] = useState(Math.round(report.max_bytes / 1_000_000_000));
  const [maxAgeDays, setMaxAgeDays] = useState(report.max_age_days);
  const [imagesShare, setImagesShare] = useState(report.images_share_percent);

  const dirty =
    maxGB !== Math.round(report.max_bytes / 1_000_000_000) ||
    maxAgeDays !== report.max_age_days ||
    imagesShare !== report.images_share_percent;

  return (
    <form
      className="flex flex-wrap items-end gap-3 border-t border-border-subtle pt-2.5 text-xs text-ink-muted"
      onSubmit={(event) => {
        event.preventDefault();
        onSave({
          max_bytes: maxGB * 1_000_000_000,
          max_age_days: maxAgeDays,
          images_share_percent: imagesShare,
          metadata_share_percent: 100 - imagesShare,
        });
      }}
    >
      <label className="flex flex-col gap-1">
        Max size (GB)
        <input
          type="number"
          min={1}
          value={maxGB}
          disabled={busy}
          onChange={(event) => setMaxGB(Number(event.target.value))}
          className={inputStyle}
        />
      </label>
      <label className="flex flex-col gap-1">
        Max age (days)
        <input
          type="number"
          min={1}
          value={maxAgeDays}
          disabled={busy}
          onChange={(event) => setMaxAgeDays(Number(event.target.value))}
          className={inputStyle}
        />
      </label>
      <label className="flex flex-col gap-1">
        Images share (%)
        <input
          type="number"
          min={0}
          max={100}
          value={imagesShare}
          disabled={busy}
          onChange={(event) => setImagesShare(Number(event.target.value))}
          className={inputStyle}
        />
      </label>
      <span>metadata gets {100 - imagesShare}%</span>
      {dirty && (
        <button
          type="submit"
          disabled={busy}
          className="rounded-md bg-accent px-2 py-1 text-accent-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          Save
        </button>
      )}
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
  "w-20 rounded-md border border-border-subtle bg-canvas px-1.5 py-1 text-xs text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
