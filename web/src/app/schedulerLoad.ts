/** How often each hour of a week is a task's turn, from its five-field cron cadences: the data of the scheduler's heatmap. */

export const WEEK_HOURS = 7 * 24;

function expand(field: string, low: number, high: number): Set<number> | null {
  const values = new Set<number>();
  for (const part of field.split(",")) {
    const [range = "", step = "1"] = part.split("/");
    const every = Number(step);
    if (!Number.isInteger(every) || every < 1) return null;
    let from = low;
    let to = high;
    if (range !== "*") {
      const [start = "", end] = range.split("-");
      from = Number(start);
      to = end === undefined ? (part.includes("/") ? high : from) : Number(end);
    }
    if (!Number.isInteger(from) || !Number.isInteger(to) || from < low || to > high || from > to) return null;
    for (let value = from; value <= to; value += every) values.add(value);
  }
  return values;
}

/** Runs per hour for each slot of a week, Sunday first (cron's own numbering); a monthly or yearly cadence counts as its share of a week. Empty when the cadence is unreadable. */
export function weekLoad(cadence: string): number[] {
  const load = new Array<number>(WEEK_HOURS).fill(0);
  const fields = cadence.trim().split(/\s+/);
  if (fields.length !== 5) return load;
  const minutes = expand(fields[0]!, 0, 59);
  const hours = expand(fields[1]!, 0, 23);
  const days = expand(fields[2]!, 1, 31);
  const months = expand(fields[3]!, 1, 12);
  const weekdays = expand(fields[4]!.replace(/7/g, "0"), 0, 6);
  if (!minutes || !hours || !days || !months || !weekdays) return load;
  const share = (fields[2] === "*" ? 1 : days.size / 31) * (fields[3] === "*" ? 1 : months.size / 12);
  for (const weekday of weekdays) {
    for (const hour of hours) load[weekday * 24 + hour]! += minutes.size * share;
  }
  return load;
}

/** The sum over several cadences of one task. */
export function taskLoad(cadences: readonly string[]): number[] {
  const total = new Array<number>(WEEK_HOURS).fill(0);
  for (const cadence of cadences) weekLoad(cadence).forEach((runs, slot) => (total[slot]! += runs));
  return total;
}

export const GREEN_BELOW_MS = 60_000;
/** A task that recorded a run faster than this is left off the heatmap. */
export const NEGLIGIBLE_TASK_MS = 20;

/** Nothing running is white; up to a minute of run time stays green, then it turns yellow to red at the busiest slot. */
export function heatColor(runtimeMs: number, busiestMs: number): string {
  if (runtimeMs <= 0) return "#ffffff";
  if (runtimeMs <= GREEN_BELOW_MS || busiestMs <= GREEN_BELOW_MS) return "hsl(120 75% 50%)";
  const share = Math.min(1, (runtimeMs - GREEN_BELOW_MS) / (busiestMs - GREEN_BELOW_MS));
  return `hsl(${Math.round(120 * (1 - share))} 75% 50%)`;
}

/** What a run of each task is expected to take: its last recorded duration, else the mean of the recorded ones, else a second. */
export function expectedDurations(tasks: readonly { last_duration_ms?: number | null }[]): number[] {
  const recorded = tasks.flatMap((task) => (typeof task.last_duration_ms === "number" && task.last_duration_ms > 0 ? [task.last_duration_ms] : []));
  const fallback = recorded.length > 0 ? recorded.reduce((sum, ms) => sum + ms, 0) / recorded.length : 1000;
  return tasks.map((task) => (typeof task.last_duration_ms === "number" && task.last_duration_ms > 0 ? task.last_duration_ms : fallback));
}

/** "850 ms", "12.4 s", "3.5 min" for a total run time. */
export function formatRuntime(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${(ms / 60_000).toFixed(1)} min`;
}

export type SortKey = "next" | "last" | "duration";
export type SortDirection = "asc" | "desc";

export const SORT_KEYS: readonly SortKey[] = ["next", "last", "duration"];

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
    if (key !== undefined) {
      const left = sortValue(a, key);
      const right = sortValue(b, key);
      if (left !== right) {
        if (left === undefined) return 1;
        if (right === undefined) return -1;
        return direction === "asc" ? left - right : right - left;
      }
    }
    return a.name.localeCompare(b.name);
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
