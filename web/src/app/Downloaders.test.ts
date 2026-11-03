import { expect, test } from "vitest";

import { formatSpeed } from "./Downloaders";

test("a client's speed reads in megabytes from 1 MB/s and kilobytes below", () => {
  expect([formatSpeed(52_340_000), formatSpeed(1_000_000), formatSpeed(640_000), formatSpeed(0)]).toEqual(["52.3 MB/s", "1.0 MB/s", "640 KB/s", "0 KB/s"]);
});
