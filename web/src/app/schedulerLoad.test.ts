import { expect, test } from "vitest";

import { taskLoad, weekLoad, WEEK_HOURS } from "./schedulerLoad";

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
