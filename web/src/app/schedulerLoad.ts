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

/** Nothing running is white; the slot with the most run time is red, a tenth of it or less green, with yellow between. */
export function heatColor(runtimeMs: number, busiestMs: number): string {
  if (runtimeMs <= 0) return "#ffffff";
  const share = busiestMs <= 0 ? 0 : Math.min(1, runtimeMs / busiestMs);
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
