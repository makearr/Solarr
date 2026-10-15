import { describe, expect, it } from "vitest";

import { appendSample, sparkPath, totalSpeed } from "./speedHistory";

describe("appendSample", () => {
  it("keeps the newest readings and skips a repeated one", () => {
    let samples = appendSample([], { at: 1, bytesPerSec: 10 }, 2);
    samples = appendSample(samples, { at: 1, bytesPerSec: 10 }, 2);
    samples = appendSample(samples, { at: 2, bytesPerSec: 20 }, 2);
    samples = appendSample(samples, { at: 3, bytesPerSec: 30 }, 2);
    expect(samples).toEqual([{ at: 2, bytesPerSec: 20 }, { at: 3, bytesPerSec: 30 }]);
  });
});

describe("totalSpeed", () => {
  it("adds only clients that are running and answered", () => {
    expect(
      totalSpeed([
        { paused: false, speed_bytes_per_sec: 100 },
        { paused: true, speed_bytes_per_sec: 50 },
        { paused: false, speed_bytes_per_sec: 25, error: "unreachable" },
        { paused: false, speed_bytes_per_sec: 7 },
      ]),
    ).toBe(107);
  });
});

describe("sparkPath", () => {
  it("draws the peak at the top and closes the area along the bottom", () => {
    expect(sparkPath([{ at: 1, bytesPerSec: 0 }, { at: 2, bytesPerSec: 50 }, { at: 3, bytesPerSec: 100 }], 100, 20)).toBe(
      "M0,20 L0.0,20.0 L50.0,10.0 L100.0,0.0 L100,20 Z",
    );
    expect(sparkPath([{ at: 1, bytesPerSec: 5 }], 100, 20)).toBe("");
  });
});
