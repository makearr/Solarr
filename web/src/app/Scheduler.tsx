import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Play } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type Schedule = components["schemas"]["Schedule"];

/** todo 4.19, ADR-59: every periodic action this process runs, made visible
 *  and runnable from the interface -- seven schedules existed with no
 *  screen before this. Run now is also what closes todo 4.8's own
 *  "trigger a scan, a probe sweep or a metadata refresh from the interface". */
export function Scheduler() {
  const queryClient = useQueryClient();
  const schedules = useQuery({
    queryKey: ["schedules"],
    queryFn: async () => {
      const { data, error } = await api.GET("/schedules");
      if (error) throw error;
      return data;
    },
    refetchInterval: 15_000,
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["schedules"] });

  const update = useMutation({
    mutationFn: async (args: { key: string; cadence?: string; enabled?: boolean }) => {
      const { error } = await api.PATCH("/schedules/{key}", {
        params: { path: { key: args.key } },
        body: { cadence: args.cadence, enabled: args.enabled },
      });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  const run = useMutation({
    mutationFn: async (key: string) => {
      const { error } = await api.POST("/schedules/{key}/run", { params: { path: { key } } });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  if (schedules.error !== null) return null;

  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Scheduler</h2>
      <p className="text-xs text-ink-muted">
        Every periodic action this process runs. Cadence is a five-field cron expression; Run now
        fires immediately and is refused, not double-queued, while a fire is already in flight.
      </p>

      {schedules.data && schedules.data.length > 0 ? (
        <ul className="flex flex-col gap-2">
          {schedules.data.map((schedule) => (
            <ScheduleRow
              key={schedule.key}
              schedule={schedule}
              onSaveCadence={(cadence) => update.mutate({ key: schedule.key, cadence })}
              onToggleEnabled={() => update.mutate({ key: schedule.key, enabled: !schedule.enabled })}
              onRunNow={() => run.mutate(schedule.key)}
              busy={update.isPending || run.isPending}
            />
          ))}
        </ul>
      ) : (
        <p className="rounded-lg border border-border-subtle bg-surface p-3 text-xs text-ink-muted">
          No task is registered.
        </p>
      )}

      {(update.isError || run.isError) && (
        <p className="text-xs text-danger">That change was refused.</p>
      )}
    </section>
  );
}

function ScheduleRow({
  schedule,
  onSaveCadence,
  onToggleEnabled,
  onRunNow,
  busy,
}: {
  schedule: Schedule;
  onSaveCadence: (cadence: string) => void;
  onToggleEnabled: () => void;
  onRunNow: () => void;
  busy: boolean;
}) {
  const [cadence, setCadence] = useState(schedule.cadence);
  const dirty = cadence !== schedule.cadence;

  return (
    <li className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">{schedule.name}</span>
        <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted">
          {schedule.tier}
        </span>
        {schedule.last_outcome && (
          <span
            className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${
              schedule.last_outcome === "error"
                ? "border-danger text-danger"
                : "border-border-subtle text-ink-muted"
            }`}
          >
            {schedule.last_outcome}
          </span>
        )}

        <button
          type="button"
          onClick={onRunNow}
          disabled={busy}
          className="ml-auto flex shrink-0 items-center gap-1 rounded-md border border-border-subtle
                     px-2 py-1 text-xs text-ink-muted hover:text-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          <Play aria-hidden className="size-3.5" />
          Run now
        </button>
      </div>

      <div className="flex flex-wrap items-center gap-3 text-xs text-ink-muted">
        <label className="flex items-center gap-1.5">
          <input
            type="checkbox"
            checked={schedule.enabled}
            disabled={busy}
            onChange={onToggleEnabled}
            className="size-3.5"
          />
          enabled
        </label>

        <label className="flex items-center gap-1.5">
          cadence
          <input
            value={cadence}
            disabled={busy}
            onChange={(event) => setCadence(event.target.value)}
            className="rounded-md border border-border-subtle bg-canvas px-1.5 py-1 font-mono text-xs
                       text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                       focus-visible:outline-accent"
          />
        </label>
        {dirty && (
          <button
            type="button"
            onClick={() => onSaveCadence(cadence)}
            disabled={busy}
            className="rounded-md bg-accent px-2 py-1 text-accent-ink disabled:opacity-50
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            Save
          </button>
        )}

        <span className="ml-auto">
          {schedule.last_run_at
            ? `last ran ${new Date(schedule.last_run_at).toLocaleString()}` +
              (schedule.last_duration_ms !== undefined ? ` (${schedule.last_duration_ms}ms)` : "")
            : "never run"}
          {schedule.next_run_at && ` · next ${new Date(schedule.next_run_at).toLocaleString()}`}
        </span>
      </div>

      {schedule.last_outcome === "error" && schedule.last_error && (
        <p className="text-xs text-danger">{schedule.last_error}</p>
      )}
    </li>
  );
}
