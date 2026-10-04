import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Play, Plus, X } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { isTierVisible, useTier } from "@/lib/useTier";

type Schedule = components["schemas"]["Schedule"];
type BackupRetention = components["schemas"]["BackupRetention"];

/** A cadence expressed the way a person thinks about it, not as a five-field cron string (asked
 *  for 2026-09-11, Jellyfin's trigger editor as the reference; weekly and monthly 2026-09-19).
 *  Anything else stays editable as "Advanced" rather than becoming unrepresentable. */
type Trigger =
  | { mode: "minutes"; minutes: number }
  | { mode: "hours"; hours: number; minute: number }
  | { mode: "daily"; hour: number; minute: number }
  | { mode: "weekly"; weekday: number; hour: number; minute: number }
  | { mode: "monthly"; day: number; hour: number; minute: number }
  | { mode: "advanced"; cadence: string };

const WEEKDAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

const number = /^\d+$/;

export function parseCadence(cadence: string): Trigger {
  const fields = cadence.trim().split(/\s+/);
  if (fields.length !== 5) return { mode: "advanced", cadence };
  const [minute, hour, dayOfMonth, month, dayOfWeek] = fields as [string, string, string, string, string];
  if (month !== "*") return { mode: "advanced", cadence };

  // A bare "*" is every minute: Check Downloads' own default read as "Advanced" (2026-09-25).
  const everyMinutes = minute === "*" ? ["*", "1"] : /^\*\/(\d+)$/.exec(minute);
  if (everyMinutes && hour === "*" && dayOfMonth === "*" && dayOfWeek === "*") {
    return { mode: "minutes", minutes: Number(everyMinutes[1]) };
  }
  if (!number.test(minute)) return { mode: "advanced", cadence };
  const at = { minute: Number(minute) };
  if (dayOfMonth === "*" && dayOfWeek === "*") {
    if (hour === "*") return { mode: "hours", hours: 1, ...at };
    const everyHours = /^\*\/(\d+)$/.exec(hour);
    if (everyHours) return { mode: "hours", hours: Number(everyHours[1]), ...at };
    if (number.test(hour)) return { mode: "daily", hour: Number(hour), ...at };
  }
  if (!number.test(hour)) return { mode: "advanced", cadence };
  if (dayOfMonth === "*" && /^[0-6]$/.test(dayOfWeek)) {
    return { mode: "weekly", weekday: Number(dayOfWeek), hour: Number(hour), ...at };
  }
  if (dayOfWeek === "*" && number.test(dayOfMonth)) {
    return { mode: "monthly", day: Number(dayOfMonth), hour: Number(hour), ...at };
  }
  return { mode: "advanced", cadence };
}

function cadenceOf(trigger: Trigger): string {
  switch (trigger.mode) {
    case "minutes":
      return `*/${trigger.minutes} * * * *`;
    case "hours":
      return trigger.hours === 1 ? `${trigger.minute} * * * *` : `${trigger.minute} */${trigger.hours} * * *`;
    case "daily":
      return `${trigger.minute} ${trigger.hour} * * *`;
    case "weekly":
      return `${trigger.minute} ${trigger.hour} * * ${trigger.weekday}`;
    case "monthly":
      return `${trigger.minute} ${trigger.hour} ${trigger.day} * *`;
    case "advanced":
      return trigger.cadence;
  }
}

const clock = (hour: number, minute: number) =>
  `${String(hour).padStart(2, "0")}:${String(minute).padStart(2, "0")}`;

function describeTrigger(trigger: Trigger): string {
  switch (trigger.mode) {
    case "minutes":
      return trigger.minutes === 1 ? "Every minute" : `Every ${trigger.minutes} minutes`;
    case "hours": {
      const past = trigger.minute === 0 ? "on the hour" : `at :${String(trigger.minute).padStart(2, "0")}`;
      return trigger.hours === 1 ? `Every hour, ${past}` : `Every ${trigger.hours} hours, ${past}`;
    }
    case "daily":
      return `Daily at ${clock(trigger.hour, trigger.minute)}`;
    case "weekly":
      return `${WEEKDAYS[trigger.weekday]}s at ${clock(trigger.hour, trigger.minute)}`;
    case "monthly":
      return `Day ${trigger.day} of each month at ${clock(trigger.hour, trigger.minute)}`;
    case "advanced":
      return trigger.cadence;
  }
}

const CATEGORIES: readonly { id: Schedule["category"]; label: string }[] = [
  { id: "library", label: "Library" },
  { id: "metadata", label: "Metadata" },
  { id: "downloads", label: "Downloads" },
  { id: "statistics", label: "Statistics" },
  { id: "maintenance", label: "Maintenance" },
];

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
  const tier = useTier();
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
    mutationFn: async (args: { key: string; cadences?: string[]; enabled?: boolean; runAtStartup?: boolean }) => {
      const { error } = await api.PATCH("/schedules/{key}", {
        params: { path: { key: args.key } },
        body: { cadences: args.cadences, enabled: args.enabled, run_at_startup: args.runAtStartup },
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
  const visible = (schedules.data ?? []).filter((schedule) => isTierVisible(tier, schedule.tier));

  return (
    <section className="flex flex-col gap-4">
      <p className="text-xs text-ink-muted">
        Every periodic action Solarr runs. A task can run at several times; Run now fires at once and
        is refused, not queued twice, while it is already running.
      </p>

      {visible.length === 0 && (
        <p className="rounded-lg border border-border-subtle bg-surface p-3 text-xs text-ink-muted">
          No task is registered.
        </p>
      )}

      {CATEGORIES.map((category) => {
        const tasks = visible.filter((schedule) => schedule.category === category.id);
        if (tasks.length === 0) return null;
        return (
          <div key={category.id} className="flex flex-col gap-2">
            <h2 className="text-sm font-semibold text-ink">{category.label}</h2>
            <ul className="flex flex-col gap-2">
              {tasks.map((schedule) => (
                <ScheduleRow
                  key={`${schedule.key}:${schedule.cadences.join("|")}`}
                  schedule={schedule}
                  onSaveCadences={(cadences) => update.mutate({ key: schedule.key, cadences })}
                  onToggleEnabled={() => update.mutate({ key: schedule.key, enabled: !schedule.enabled })}
                  onToggleStartup={() =>
                    update.mutate({ key: schedule.key, runAtStartup: !schedule.run_at_startup })
                  }
                  onRunNow={() => run.mutate({ key: schedule.key })}
                  onRunFull={() => run.mutate({ key: schedule.key, full: true })}
                  busy={update.isPending || run.isPending}
                />
              ))}
            </ul>
          </div>
        );
      })}

      {(update.isError || run.isError) && (
        <p className="text-xs text-danger">That change was refused.</p>
      )}
    </section>
  );
}

function ScheduleRow({
  schedule,
  onSaveCadences,
  onToggleEnabled,
  onToggleStartup,
  onRunNow,
  onRunFull,
  busy,
}: {
  schedule: Schedule;
  onSaveCadences: (cadences: string[]) => void;
  onToggleEnabled: () => void;
  onToggleStartup: () => void;
  onRunNow: () => void;
  onRunFull: () => void;
  busy: boolean;
}) {
  const [triggers, setTriggers] = useState<Trigger[]>(() => schedule.cadences.map(parseCadence));
  const cadences = triggers.map(cadenceOf);
  const dirty = cadences.join("|") !== schedule.cadences.join("|");

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

        <span className="ml-auto">
          {schedule.last_run_at
            ? `last ran ${new Date(schedule.last_run_at).toLocaleString()}` +
              (schedule.last_duration_ms !== undefined ? ` (${schedule.last_duration_ms}ms)` : "")
            : "never run"}
          {schedule.next_run_at && ` · next ${new Date(schedule.next_run_at).toLocaleString()}`}
        </span>
      </div>

      <div className="flex flex-col gap-1.5 text-xs text-ink-muted">
        {schedule.run_at_startup && (
          <span className="flex flex-wrap items-center gap-1.5">
            <span className="rounded-md border border-border-subtle bg-canvas px-2 py-0.5 text-ink">At startup</span>
            <button
              type="button"
              aria-label="Remove schedule at startup"
              disabled={busy}
              onClick={onToggleStartup}
              className="rounded p-0.5 text-ink-muted hover:text-danger disabled:opacity-50"
            >
              <X aria-hidden className="size-3.5" />
            </button>
          </span>
        )}
        {triggers.map((trigger, index) => (
          <span key={index} className="flex flex-wrap items-center gap-1.5">
            <TriggerEditor
              trigger={trigger}
              busy={busy}
              onChange={(next) => setTriggers(triggers.map((entry, at) => (at === index ? next : entry)))}
            />
            {triggers.length > 1 && (
              <button
                type="button"
                aria-label={`Remove schedule ${describeTrigger(trigger)}`}
                disabled={busy}
                onClick={() => setTriggers(triggers.filter((_, at) => at !== index))}
                className="rounded p-0.5 text-ink-muted hover:text-danger disabled:opacity-50"
              >
                <X aria-hidden className="size-3.5" />
              </button>
            )}
          </span>
        ))}
        <span className="flex items-center gap-2">
          <button
            type="button"
            disabled={busy || triggers.length >= 12}
            onClick={() => setTriggers([...triggers, { mode: "daily", hour: 12, minute: 0 }])}
            className="flex w-fit items-center gap-1 rounded-md border border-border-subtle px-2 py-0.5
                       hover:text-ink disabled:opacity-50"
          >
            <Plus aria-hidden className="size-3.5" />
            Add a time
          </button>
          {!schedule.run_at_startup && (
            <button
              type="button"
              disabled={busy}
              onClick={onToggleStartup}
              title="Also run once whenever Solarr starts. Database maintenance and backups wait a few minutes and are skipped when they ran recently."
              className="flex w-fit items-center gap-1 rounded-md border border-border-subtle px-2 py-0.5
                         hover:text-ink disabled:opacity-50"
            >
              <Plus aria-hidden className="size-3.5" />
              Add at startup
            </button>
          )}
          {dirty && (
            <>
              <button
                type="button"
                onClick={() => onSaveCadences(cadences)}
                disabled={busy}
                className="rounded-md bg-accent px-2 py-0.5 text-accent-ink disabled:opacity-50
                           focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
              >
                Save
              </button>
              <button
                type="button"
                onClick={() => setTriggers(schedule.cadences.map(parseCadence))}
                disabled={busy}
                className="rounded-md border border-border-subtle px-2 py-0.5 hover:text-ink"
              >
                Undo
              </button>
            </>
          )}
          {schedule.cadences.join("|") !== schedule.default_cadence && !dirty && (
            <button
              type="button"
              onClick={() => onSaveCadences([schedule.default_cadence])}
              disabled={busy}
              title={describeTrigger(parseCadence(schedule.default_cadence))}
              className="rounded-md border border-border-subtle px-2 py-0.5 hover:text-ink"
            >
              Reset to default
            </button>
          )}
        </span>
      </div>

      {schedule.key === "backup_cleanup" && <BackupRetentionEditor />}

      {schedule.last_outcome === "error" && schedule.last_error && (
        <p className="text-xs text-danger">{schedule.last_error}</p>
      )}
    </li>
  );
}

const triggerInputStyle =
  "rounded-md border border-border-subtle bg-canvas px-1.5 py-1 text-xs text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";

/** One trigger's editor; "Advanced" edits the raw cron expression for any shape the others cannot show. */
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
              onChange({ mode, hours: 1, minute: 0 });
              break;
            case "daily":
              onChange({ mode, hour: 3, minute: 0 });
              break;
            case "weekly":
              onChange({ mode, weekday: 0, hour: 3, minute: 0 });
              break;
            case "monthly":
              onChange({ mode, day: 1, hour: 3, minute: 0 });
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
        <option value="weekly">Weekly on</option>
        <option value="monthly">Monthly on day</option>
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
        <>
          <input
            type="number"
            min={1}
            max={23}
            aria-label="Hours between runs"
            value={trigger.hours}
            disabled={busy}
            onChange={(event) => onChange({ ...trigger, hours: Number(event.target.value) || 1 })}
            className={`${triggerInputStyle} w-16`}
          />
          at minute
          <input
            type="number"
            min={0}
            max={59}
            aria-label="Minute past the hour"
            value={trigger.minute}
            disabled={busy}
            onChange={(event) => onChange({ ...trigger, minute: Math.min(59, Number(event.target.value) || 0) })}
            className={`${triggerInputStyle} w-16`}
          />
        </>
      )}
      {trigger.mode === "weekly" && (
        <select
          aria-label="Day of the week"
          value={trigger.weekday}
          disabled={busy}
          onChange={(event) => onChange({ ...trigger, weekday: Number(event.target.value) })}
          className={triggerInputStyle}
        >
          {WEEKDAYS.map((day, index) => (
            <option key={day} value={index}>
              {day}
            </option>
          ))}
        </select>
      )}
      {trigger.mode === "monthly" && (
        <input
          type="number"
          min={1}
          max={28}
          aria-label="Day of the month"
          title="Up to the 28th, so it runs every month"
          value={trigger.day}
          disabled={busy}
          onChange={(event) =>
            onChange({ ...trigger, day: Math.min(28, Math.max(1, Number(event.target.value) || 1)) })
          }
          className={`${triggerInputStyle} w-16`}
        />
      )}
      {(trigger.mode === "daily" || trigger.mode === "weekly" || trigger.mode === "monthly") && (
        <input
          type="time"
          aria-label="Time of day"
          value={clock(trigger.hour, trigger.minute)}
          disabled={busy}
          onChange={(event) => {
            const [hour, minute] = event.target.value.split(":").map(Number);
            onChange({ ...trigger, hour: hour || 0, minute: minute || 0 });
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

/** The Clean old backups rule: the newest N, or everything younger than N days (asked for 2026-09-19). */
function BackupRetentionEditor() {
  const queryClient = useQueryClient();
  const saved = useQuery({
    queryKey: ["settings", "backups"],
    queryFn: async () => {
      const { data, error } = await api.GET("/settings/backups");
      if (error) throw error;
      return data;
    },
  });
  const save = useMutation({
    mutationFn: async (body: BackupRetention) => {
      const { error } = await api.PUT("/settings/backups", { body });
      if (error) throw error;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["settings", "backups"] }),
  });
  const [draft, setDraft] = useState<BackupRetention | null>(null);

  if (!saved.data) return null;
  const current = draft ?? saved.data;
  const dirty =
    draft !== null &&
    (draft.mode !== saved.data.mode ||
      draft.keep_count !== saved.data.keep_count ||
      draft.keep_days !== saved.data.keep_days);

  return (
    <div className="flex flex-wrap items-center gap-1.5 text-xs text-ink-muted">
      Keep
      <select
        aria-label="Keep backups by"
        value={current.mode}
        disabled={save.isPending}
        onChange={(event) => setDraft({ ...current, mode: event.target.value as BackupRetention["mode"] })}
        className={triggerInputStyle}
      >
        <option value="count">the last</option>
        <option value="days">the backups of the last</option>
      </select>
      {current.mode === "count" ? (
        <>
          <input
            type="number"
            min={3}
            max={1000}
            aria-label="Backups to keep"
            value={current.keep_count}
            disabled={save.isPending}
            onChange={(event) => setDraft({ ...current, keep_count: Number(event.target.value) || 3 })}
            className={`${triggerInputStyle} w-16`}
          />
          backups of each database
        </>
      ) : (
        <>
          <input
            type="number"
            min={1}
            max={3650}
            aria-label="Days to keep backups"
            value={current.keep_days}
            disabled={save.isPending}
            onChange={(event) => setDraft({ ...current, keep_days: Number(event.target.value) || 1 })}
            className={`${triggerInputStyle} w-16`}
          />
          days, and always the newest 3
        </>
      )}
      {dirty && (
        <>
          <button
            type="button"
            onClick={() => save.mutate(current, { onSuccess: () => setDraft(null) })}
            disabled={save.isPending}
            className="rounded-md bg-accent px-2 py-0.5 text-accent-ink disabled:opacity-50
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            Save
          </button>
          <button
            type="button"
            onClick={() => setDraft(null)}
            disabled={save.isPending}
            className="rounded-md border border-border-subtle px-2 py-0.5 hover:text-ink"
          >
            Undo
          </button>
        </>
      )}
      {save.isError && <span className="text-danger">Refused: at least 3 backups, at least 1 day.</span>}
    </div>
  );
}
