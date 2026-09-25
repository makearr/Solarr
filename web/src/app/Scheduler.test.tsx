import { expect, test } from "vitest";

import { parseCadence } from "./Scheduler";

test("every minute reads as Every, not as a cron expression", () => {
  expect(parseCadence("* * * * *")).toEqual({ mode: "minutes", minutes: 1 });
  expect(parseCadence("*/5 * * * *")).toEqual({ mode: "minutes", minutes: 5 });
});
