import { expect, test } from "vitest";

import { formatBytes, formatDuration, statusLabel } from "./Queue";

const item = { id: 1, release_title: "Heka-Zanuvode", client: "SABnzbd", added_at: "2026-09-29T00:00:00Z" };

test("the status says what the client says, and Solarr's own steps once it has the files", () => {
  expect(statusLabel({ ...item, state: "QUEUED" })).toBe("Queued");
  expect(statusLabel({ ...item, state: "QUEUED", client_state: "paused" })).toBe("Paused");
  expect(statusLabel({ ...item, state: "DOWNLOADING", client_state: "processing" })).toBe("Unpacking");
  expect(statusLabel({ ...item, state: "COMPLETED", error: "outside every download folder" })).toBe("Downloaded, waiting");
  expect(statusLabel({ ...item, state: "IMPORTING", client_state: "completed" })).toBe("Importing");
});

test("sizes and times read as a person would say them", () => {
  expect(formatBytes(512)).toBe("512 B");
  expect(formatBytes(1536 * 1024)).toBe("1.5 MB");
  expect(formatBytes(700 * 1024 * 1024)).toBe("700 MB");
  expect(formatDuration(30)).toBe("under a minute");
  expect(formatDuration(600)).toBe("10 min");
  expect(formatDuration(3900)).toBe("1 h 5 min");
});
