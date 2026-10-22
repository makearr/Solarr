import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";

import { api, ok } from "@/api/client";
import { isTierVisible, useTier } from "@/lib/useTier";
import type { components } from "@/api/schema";

import { Card, Field, SaveBar } from "./SettingsCard";

type CacheReport = components["schemas"]["CacheReport"];

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
    mutationFn: async (budget: {
      max_bytes: number;
      max_age_days: number;
      images_share_percent: number;
      metadata_share_percent: number;
    }) => {
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
          {report.data.measured_at
            ? `Measured ${new Date(report.data.measured_at).toLocaleString()}.`
            : "Not measured yet: the cache maintenance task has not run."}
        </p>
      </Card>

      <BudgetForm report={report.data} onSave={(budget) => update.mutate(budget)} busy={update.isPending} />
      {update.isError && <p className="text-sm text-danger">That budget was refused.</p>}

      <Card title="Clear the cache" help="Throws everything away; it is fetched again as it is needed.">
        <ClearButton onClear={() => clear.mutate()} busy={clear.isPending} />
        {clear.isError && <p className="text-xs text-danger">The cache could not be cleared.</p>}
      </Card>
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

  const submit = () =>
    onSave({
      max_bytes: maxGB * 1_000_000_000,
      max_age_days: maxAgeDays,
      images_share_percent: imagesShare,
      metadata_share_percent: 100 - imagesShare,
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
      <Card title="Limits" help="The cache is trimmed when it passes its size, and anything older than the age is dropped.">
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
