import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { throttle } from "./throttle";

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

test("ten calls within one second run twice: at once and once trailing", () => {
  const run = vi.fn();
  const throttled = throttle(run, 2000);

  for (let i = 0; i < 10; i++) {
    throttled.call();
    vi.advanceTimersByTime(100);
  }
  expect(run).toHaveBeenCalledTimes(1);

  vi.advanceTimersByTime(2000);
  expect(run).toHaveBeenCalledTimes(2);

  vi.advanceTimersByTime(10_000);
  expect(run).toHaveBeenCalledTimes(2);
});

test("a call after a quiet window runs at once", () => {
  const run = vi.fn();
  const throttled = throttle(run, 2000);
  throttled.call();
  vi.advanceTimersByTime(5000);
  throttled.call();
  expect(run).toHaveBeenCalledTimes(2);
});

test("cancel drops the trailing run", () => {
  const run = vi.fn();
  const throttled = throttle(run, 2000);
  throttled.call();
  throttled.call();
  throttled.cancel();
  vi.advanceTimersByTime(5000);
  expect(run).toHaveBeenCalledTimes(1);
});
