import { expect, test } from "vitest";

import { expectedDurations, formatRuntime, heatColor, parseSortKey, sortTasks, taskLoad, weekLoad, WEEK_HOURS } from "./schedulerLoad";

const total = (load: number[]) => load.reduce((sum, runs) => sum + runs, 0);

test("a daily cadence runs once in that hour of every weekday", () => {
  const load = weekLoad("30 3 * * *");
  expect(total(load)).toBe(7);
  expect(load[3]).toBe(1);
  expect(load[24 * 6 + 3]).toBe(1);
});

test("every ten minutes is six runs in each hour", () => {
  expect(weekLoad("*/10 * * * *").every((runs) => runs === 6)).toBe(true);
});

test("a weekly cadence names its weekday; 7 is Sunday too", () => {
  expect(weekLoad("0 4 * * 1")[24 + 4]).toBe(1);
  expect(weekLoad("0 4 * * 7")[4]).toBe(1);
});

test("an hour step and a range of weekdays", () => {
  const load = weekLoad("15 */6 * * 1-5");
  expect(total(load)).toBe(4 * 5);
  expect(load[24 + 12]).toBe(1);
  expect(load[0]).toBe(0);
});

test("a monthly cadence counts as its share of a week", () => {
  expect(total(weekLoad("0 2 1 * *"))).toBeCloseTo((7 * 1) / 31, 5);
});

test("nonsense counts for nothing, and a task adds its cadences", () => {
  expect(total(weekLoad("not a cron"))).toBe(0);
  expect(total(weekLoad("99 * * * *"))).toBe(0);
  expect(total(taskLoad(["0 1 * * *", "0 2 * * *"]))).toBe(14);
  expect(taskLoad([]).length).toBe(WEEK_HOURS);
});

test("heat colour: white when idle, green up to a minute, then yellow to red at the busiest slot", () => {
  expect(heatColor(0, 300_000)).toBe("#ffffff");
  expect(heatColor(59_000, 300_000)).toBe("hsl(120 75% 50%)");
  expect(heatColor(300_000, 300_000)).toBe("hsl(0 75% 50%)");
  expect(heatColor(180_000, 300_000)).toBe("hsl(60 75% 50%)");
  expect(heatColor(200_000, 50_000)).toBe("hsl(120 75% 50%)");
});

test("a task without a recorded duration is expected to take the mean of the others", () => {
  expect(expectedDurations([{ last_duration_ms: 100 }, {}, { last_duration_ms: 300 }])).toEqual([100, 200, 300]);
  expect(expectedDurations([{}])).toEqual([1000]);
});

test("run times read in the unit that fits", () => {
  expect([formatRuntime(850), formatRuntime(12_400), formatRuntime(210_000)]).toEqual(["850 ms", "12.4 s", "3.5 min"]);
});

test("tasks sort by next run both ways, missing values last, ties by name", () => {
  const tasks = [
    { name: "c", next_run_at: "2026-01-02T00:00:00Z" },
    { name: "b" },
    { name: "a", next_run_at: "2026-01-01T00:00:00Z" },
  ];
  expect(sortTasks(tasks, "next", "asc").map((t) => t.name)).toEqual(["a", "c", "b"]);
  expect(sortTasks(tasks, "next", "desc").map((t) => t.name)).toEqual(["c", "a", "b"]);
  expect(sortTasks(tasks, undefined, "asc").map((t) => t.name)).toEqual(["a", "b", "c"]);
});

test("tasks sort by last run and by last duration", () => {
  const tasks = [
    { name: "x", last_run_at: "2026-01-01T00:00:00Z", last_duration_ms: 50 },
    { name: "y", last_run_at: "2026-02-01T00:00:00Z", last_duration_ms: 10 },
  ];
  expect(sortTasks(tasks, "last", "desc").map((t) => t.name)).toEqual(["y", "x"]);
  expect(sortTasks(tasks, "duration", "asc").map((t) => t.name)).toEqual(["y", "x"]);
});

test("sort keys parse strictly", () => {
  expect(parseSortKey("next")).toBe("next");
  expect(parseSortKey("bogus")).toBeUndefined();
});
