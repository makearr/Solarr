import { expect, test } from "vitest";

import { parseCadence } from "./Scheduler";

test("every minute reads as Every, not as a cron expression", () => {
  expect(parseCadence("* * * * *")).toEqual({ mode: "minutes", minutes: 1 });
  expect(parseCadence("*/5 * * * *")).toEqual({ mode: "minutes", minutes: 5 });
});

// The same table as internal/jobs/task_test.go's TestSimpleCadence: the two must agree.
test("plain triggers and raw cron are told apart as the server's registry guard does", () => {
  const cases: Record<string, boolean> = {
    "* * * * *": true, "*/15 * * * *": true, "20 * * * *": true, "0 */6 * * *": true,
    "30 2 * * *": true, "0 4 * * 0": true, "0 3 1 * *": true,
    "7,22,37,52 * * * *": false, "5-10 * * * *": false, "0 0 1 1 *": false, "*/5 2 * * *": false,
    "0 */2 * * 1": false, "0 4 * * 7": false, "0 4 1 * 1": false, "@daily": false,
  };
  for (const [cadence, simple] of Object.entries(cases)) {
    expect([cadence, parseCadence(cadence).mode !== "advanced"]).toEqual([cadence, simple]);
  }
});
