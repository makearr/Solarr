/** The scheduler's heatmap: the run time of each hour of the last seven days, from what the workers actually did. */

export const WEEK_HOURS = 7 * 24;

export const GREEN_BELOW_MS = 60_000;

export interface HistoryHour {
  hour_start: number;
  name: string;
  run_ms: number;
}

/** What one weekday-and-hour slot of the grid shows: the latest hour of the week that fell in it. */
export interface HeatSlot {
  hourStart: number;
  runtimeMs: number;
  names: string[];
}

/** The slot of a time on the grid: Sunday first, local hour. */
export function slotOf(when: Date): number {
  return when.getDay() * 24 + when.getHours();
}

/** The week's hours laid onto the 168 slots; where two hours fall in one slot (a clock change) the later wins. */
export function heatSlots(hours: readonly HistoryHour[]): (HeatSlot | undefined)[] {
  const byHour = new Map<number, HeatSlot>();
  for (const hour of hours) {
    const slot = byHour.get(hour.hour_start) ?? { hourStart: hour.hour_start, runtimeMs: 0, names: [] };
    slot.runtimeMs += hour.run_ms;
    if (hour.run_ms > 0 && !slot.names.includes(hour.name)) slot.names.push(hour.name);
    byHour.set(hour.hour_start, slot);
  }
  const slots = new Array<HeatSlot | undefined>(WEEK_HOURS).fill(undefined);
  for (const slot of byHour.values()) {
    const index = slotOf(new Date(slot.hourStart * 1000));
    const held = slots[index];
    if (!held || held.hourStart < slot.hourStart) slots[index] = slot;
  }
  return slots;
}

/** Where green ends: a minute, or the least run time any hour had when that is more, so something is always green. */
export function greenLimit(runtimes: readonly number[]): number {
  const running = runtimes.filter((ms) => ms > 0);
  return running.length === 0 ? GREEN_BELOW_MS : Math.max(GREEN_BELOW_MS, Math.min(...running));
}

/** Nothing running is white; up to the green limit stays green, then it turns yellow to red at the busiest slot. */
export function heatColor(runtimeMs: number, busiestMs: number, greenMs: number = GREEN_BELOW_MS): string {
  if (runtimeMs <= 0) return "#ffffff";
  if (runtimeMs <= greenMs || busiestMs <= greenMs) return "hsl(120 75% 50%)";
  const share = Math.min(1, (runtimeMs - greenMs) / (busiestMs - greenMs));
  return `hsl(${Math.round(120 * (1 - share))} 75% 50%)`;
}

/** "850 ms", "12.4 s", "3.5 min" for a total run time. */
export function formatRuntime(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${(ms / 60_000).toFixed(1)} min`;
}

export type SortKey = "name" | "next" | "last" | "duration";
export type SortDirection = "asc" | "desc";

export const SORT_KEYS: readonly SortKey[] = ["name", "next", "last", "duration"];

export function parseSortKey(value: unknown): SortKey | undefined {
  return SORT_KEYS.find((key) => key === value);
}

interface Sortable {
  name: string;
  next_run_at?: string | null;
  last_run_at?: string | null;
  last_duration_ms?: number | null;
}

function sortValue(task: Sortable, key: SortKey): number | undefined {
  const raw = key === "next" ? task.next_run_at : key === "last" ? task.last_run_at : task.last_duration_ms;
  if (raw === undefined || raw === null || raw === "") return undefined;
  const value = typeof raw === "number" ? raw : Date.parse(raw);
  return Number.isNaN(value) ? undefined : value;
}

/** Sorted copy; tasks without a value (never run, not scheduled) go last in either direction, ties by name. */
export function sortTasks<T extends Sortable>(tasks: readonly T[], key: SortKey | undefined, direction: SortDirection): T[] {
  const sorted = [...tasks];
  sorted.sort((a, b) => {
    if (key !== undefined && key !== "name") {
      const left = sortValue(a, key);
      const right = sortValue(b, key);
      if (left !== right) {
        if (left === undefined) return 1;
        if (right === undefined) return -1;
        return direction === "asc" ? left - right : right - left;
      }
    }
    const byName = a.name.localeCompare(b.name);
    return key === "name" && direction === "desc" ? -byName : byName;
  });
  return sorted;
}

export type RunningSortKey = "runtime" | "started" | "remaining" | "cpu" | "io";

/** Rough load of a job by its worker pool (0-3): nothing measures a job's own CPU or disk use, so the pool stands in. */
const POOL_LOAD: Readonly<Record<string, { cpu: number; io: number }>> = {
  files: { cpu: 3, io: 3 },
  scan: { cpu: 1, io: 3 },
  rescan: { cpu: 1, io: 3 },
  move: { cpu: 1, io: 3 },
  import: { cpu: 1, io: 2 },
  maintenance: { cpu: 2, io: 2 },
  metadata: { cpu: 1, io: 1 },
  search: { cpu: 1, io: 0 },
  acquire: { cpu: 0, io: 1 },
  housekeeping: { cpu: 0, io: 1 },
};

export function poolLoad(pool: string, kind: "cpu" | "io"): number {
  return POOL_LOAD[pool]?.[kind] ?? 0;
}

type SortableRun = { started_at: string; eta_seconds?: number | null; pool: string; job_id: number };

/** Orders running jobs; longest running (oldest start) first by default, an unknown time left sorts last. */
export function sortRunning<T extends SortableRun>(jobs: readonly T[], key: RunningSortKey, direction: SortDirection): T[] {
  const sign = direction === "asc" ? 1 : -1;
  const started = (job: T) => Date.parse(job.started_at);
  const rank = (job: T): number => {
    switch (key) {
      case "runtime":
        return started(job);
      case "started":
        return -started(job);
      case "remaining":
        return job.eta_seconds ?? Number.POSITIVE_INFINITY;
      case "cpu":
        return -poolLoad(job.pool, "cpu");
      case "io":
        return -poolLoad(job.pool, "io");
    }
  };
  return [...jobs].sort((a, b) => {
    const ra = rank(a);
    const rb = rank(b);
    if (ra === rb) return started(a) - started(b) || a.job_id - b.job_id;
    if (!Number.isFinite(ra)) return 1;
    if (!Number.isFinite(rb)) return -1;
    return sign * (ra - rb);
  });
}
