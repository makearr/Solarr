import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Play } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type Schedule = components["schemas"]["Schedule"];

/** A cadence expressed the way a person thinks about it, not as a five-field
 *  cron string -- asked for 2026-09-11 (Jellyfin's own Scheduled Tasks
 *  trigger editor was the reference). Only the three shapes every registered
 *  task in this codebase actually uses are given friendly editors; anything
 *  else (a real cron expression with steps, ranges or a day-of-week/month
 *  restriction) falls back to "Advanced" so it stays editable rather than
 *  becoming unrepresentable. */
type Trigger =
  | { mode: "minutes"; minutes: number }
  | { mode: "hours"; hours: number }
  | { mode: "daily"; hour: number; minute: number }
  | { mode: "advanced"; cadence: string };

function parseCadence(cadence: string): Trigger {
  const fields = cadence.trim().split(/\s+/);
  if (fields.length !== 5) return { mode: "advanced", cadence };
  const [minute, hour, dayOfMonth, month, dayOfWeek] = fields as [string, string, string, string, string];
  if (dayOfMonth !== "*" || month !== "*" || dayOfWeek !== "*") return { mode: "advanced", cadence };

  const everyMinutes = /^\*\/(\d+)$/.exec(minute);
  if (everyMinutes && hour === "*") {
    return { mode: "minutes", minutes: Number(everyMinutes[1]) };
  }
  const everyHours = /^\*\/(\d+)$/.exec(hour);
  if (everyHours && /^\d+$/.test(minute)) {
    return { mode: "hours", hours: Number(everyHours[1]) };
  }
  if (/^\d+$/.test(minute) && /^\d+$/.test(hour)) {
    return { mode: "daily", hour: Number(hour), minute: Number(minute) };
  }
  return { mode: "advanced", cadence };
}

function cadenceOf(trigger: Trigger): string {
  switch (trigger.mode) {
    case "minutes":
      return `*/${trigger.minutes} * * * *`;
    case "hours":
      return `0 */${trigger.hours} * * *`;
    case "daily":
      return `${trigger.minute} ${trigger.hour} * * *`;
    case "advanced":
      return trigger.cadence;
  }
}

function describeTrigger(trigger: Trigger): string {
  switch (trigger.mode) {
    case "minutes":
      return trigger.minutes === 1 ? "Every minute" : `Every ${trigger.minutes} minutes`;
    case "hours":
      return trigger.hours === 1 ? "Every hour" : `Every ${trigger.hours} hours`;
    case "daily":
      return `Daily at ${String(trigger.hour).padStart(2, "0")}:${String(trigger.minute).padStart(2, "0")}`;
    case "advanced":
      return trigger.cadence;
  }
}

/** todo 4.19, ADR-59: every periodic action this process runs, made visible
 *  and runnable from the interface -- seven schedules existed with no
 *  screen before this. Run now is also what closes todo 4.8's own
 *  "trigger a scan, a probe sweep or a metadata refresh from the interface". */
export function SchedulerPage() {
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <header className="flex flex-col gap-1">
          <h1 className="text-base font-semibold">Scheduler</h1>
        </header>
        <Scheduler />
      </div>
    </div>
  );
}

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
    mutationFn: async (args: { key: string; full?: boolean }) => {
      const { error } = await api.POST("/schedules/{key}/run", {
        params: { path: { key: args.key }, query: args.full ? { full: true } : undefined },
      });
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
              onRunNow={() => run.mutate({ key: schedule.key })}
              onRunFull={() => run.mutate({ key: schedule.key, full: true })}
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
  onRunFull,
  busy,
}: {
  schedule: Schedule;
  onSaveCadence: (cadence: string) => void;
  onToggleEnabled: () => void;
  onRunNow: () => void;
  onRunFull: () => void;
  busy: boolean;
}) {
  const [trigger, setTrigger] = useState<Trigger>(() => parseCadence(schedule.cadence));
  const cadence = cadenceOf(trigger);
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

        {schedule.supports_full && (
          <button
            type="button"
            onClick={onRunFull}
            disabled={busy}
            title="Re-reads every directory regardless of when it last changed, rather than only
                   what the incremental sweep would have picked up on its own."
            className="ml-auto flex shrink-0 items-center gap-1 rounded-md border border-border-subtle
                       px-2 py-1 text-xs text-ink-muted hover:text-ink disabled:opacity-50
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            <Play aria-hidden className="size-3.5" />
            Full rescan
          </button>
        )}
        <button
          type="button"
          onClick={onRunNow}
          disabled={busy}
          className={`flex shrink-0 items-center gap-1 rounded-md border border-border-subtle
                     px-2 py-1 text-xs text-ink-muted hover:text-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent
                     ${schedule.supports_full ? "" : "ml-auto"}`}
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

        <TriggerEditor trigger={trigger} onChange={setTrigger} busy={busy} />
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

const triggerInputStyle =
  "rounded-md border border-border-subtle bg-canvas px-1.5 py-1 text-xs text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";

/** The friendly editor for the three cadence shapes this codebase's own
 *  registered tasks actually use (Jellyfin 12's Scheduled Tasks trigger
 *  editor is the reference, asked for by name 2026-09-11): every N minutes,
 *  every N hours, or daily at a time. "Advanced" edits the raw cron
 *  expression directly -- reached automatically for any cadence that does
 *  not match one of the three friendly shapes, so nothing already configured
 *  becomes silently unrepresentable. */
function TriggerEditor({
  trigger,
  onChange,
  busy,
}: {
  trigger: Trigger;
  onChange: (trigger: Trigger) => void;
  busy: boolean;
}) {
  return (
    <span className="flex flex-wrap items-center gap-1.5">
      <select
        value={trigger.mode}
        disabled={busy}
        onChange={(event) => {
          const mode = event.target.value as Trigger["mode"];
          switch (mode) {
            case "minutes":
              onChange({ mode, minutes: 15 });
              break;
            case "hours":
              onChange({ mode, hours: 1 });
              break;
            case "daily":
              onChange({ mode, hour: 3, minute: 0 });
              break;
            case "advanced":
              onChange({ mode, cadence: cadenceOf(trigger) });
              break;
          }
        }}
        className={triggerInputStyle}
      >
        <option value="minutes">Every (minutes)</option>
        <option value="hours">Every (hours)</option>
        <option value="daily">Daily at</option>
        <option value="advanced">Advanced (cron)</option>
      </select>

      {trigger.mode === "minutes" && (
        <input
          type="number"
          min={1}
          max={59}
          value={trigger.minutes}
          disabled={busy}
          onChange={(event) => onChange({ mode: "minutes", minutes: Number(event.target.value) || 1 })}
          className={`${triggerInputStyle} w-16`}
        />
      )}
      {trigger.mode === "hours" && (
        <input
          type="number"
          min={1}
          max={23}
          value={trigger.hours}
          disabled={busy}
          onChange={(event) => onChange({ mode: "hours", hours: Number(event.target.value) || 1 })}
          className={`${triggerInputStyle} w-16`}
        />
      )}
      {trigger.mode === "daily" && (
        <input
          type="time"
          value={`${String(trigger.hour).padStart(2, "0")}:${String(trigger.minute).padStart(2, "0")}`}
          disabled={busy}
          onChange={(event) => {
            const [hour, minute] = event.target.value.split(":").map(Number);
            onChange({ mode: "daily", hour: hour || 0, minute: minute || 0 });
          }}
          className={triggerInputStyle}
        />
      )}
      {trigger.mode === "advanced" && (
        <input
          value={trigger.cadence}
          disabled={busy}
          onChange={(event) => onChange({ mode: "advanced", cadence: event.target.value })}
          placeholder="* * * * *"
          className={`${triggerInputStyle} font-mono`}
        />
      )}
      <span className="text-ink-muted">{describeTrigger(trigger)}</span>
    </span>
  );
}
