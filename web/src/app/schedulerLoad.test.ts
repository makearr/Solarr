import { expect, test } from "vitest";

import { formatRuntime, greenLimit, heatColor, heatSlots, parseSortKey, slotOf, sortRunning, sortTasks, WEEK_HOURS } from "./schedulerLoad";

test("heat colour: white when idle, green up to the limit, then yellow to red at the busiest slot", () => {
  expect(heatColor(0, 300_000)).toBe("#ffffff");
  expect(heatColor(59_000, 300_000)).toBe("hsl(120 75% 50%)");
  expect(heatColor(300_000, 300_000)).toBe("hsl(0 75% 50%)");
  expect(heatColor(180_000, 300_000)).toBe("hsl(60 75% 50%)");
  expect(heatColor(200_000, 50_000)).toBe("hsl(120 75% 50%)");
  expect(heatColor(2_000, 12_000, 1_000)).not.toBe("hsl(120 75% 50%)");
});

test("the green limit follows the history so that something is always green", () => {
  expect(greenLimit([0, 0])).toBe(60_000);
  expect(greenLimit([0, 5_000, 900_000])).toBe(60_000);
  expect(greenLimit([0, 400_000, 900_000])).toBe(400_000);
  const busy = [0, 400_000, 900_000];
  expect(heatColor(400_000, 900_000, greenLimit(busy))).toBe("hsl(120 75% 50%)");
  expect(heatColor(900_000, 900_000, greenLimit(busy))).toBe("hsl(0 75% 50%)");
});

test("hours land on the weekday-and-hour grid, the later week winning a shared slot, and an hour lists what ran", () => {
  const monday = new Date(2026, 10, 9, 14, 0, 0);
  const lastWeek = new Date(2026, 10, 2, 14, 0, 0);
  const slots = heatSlots([
    { hour_start: monday.getTime() / 1000, name: "Scan", run_ms: 90_000 },
    { hour_start: monday.getTime() / 1000, name: "Import", run_ms: 10_000 },
    { hour_start: monday.getTime() / 1000, name: "Idle", run_ms: 0 },
    { hour_start: lastWeek.getTime() / 1000, name: "Old", run_ms: 5_000 },
  ]);
  expect(slots).toHaveLength(WEEK_HOURS);
  expect(slots[slotOf(monday)]).toEqual({ hourStart: monday.getTime() / 1000, runtimeMs: 100_000, names: ["Scan", "Import"] });
  expect(slots.filter(Boolean)).toHaveLength(1);
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

const run = (job_id: number, started_at: string, pool: string, eta_seconds?: number) => ({ job_id, started_at, pool, eta_seconds });

test("running jobs: longest running first by default, unknown time left last", () => {
  const jobs = [run(1, "2026-11-09T05:00:00Z", "metadata", 60), run(2, "2026-11-09T01:00:00Z", "files"), run(3, "2026-11-09T03:00:00Z", "scan", 30)];
  expect(sortRunning(jobs, "runtime", "asc").map((job) => job.job_id)).toEqual([2, 3, 1]);
  expect(sortRunning(jobs, "started", "asc").map((job) => job.job_id)).toEqual([1, 3, 2]);
  expect(sortRunning(jobs, "remaining", "asc").map((job) => job.job_id)).toEqual([3, 1, 2]);
  expect(sortRunning(jobs, "remaining", "desc").map((job) => job.job_id)).toEqual([1, 3, 2]);
  expect(sortRunning(jobs, "cpu", "asc").map((job) => job.job_id)).toEqual([2, 3, 1]);
  expect(sortRunning(jobs, "io", "asc")[0]!.job_id).toBe(2);
});

test("tasks sort by name, either way", () => {
  const tasks = [{ name: "b" }, { name: "c" }, { name: "a" }];
  expect(sortTasks(tasks, "name", "asc").map((task) => task.name)).toEqual(["a", "b", "c"]);
  expect(sortTasks(tasks, "name", "desc").map((task) => task.name)).toEqual(["c", "b", "a"]);
});
