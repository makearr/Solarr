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
