import { expect, test } from "vitest";

import { describe as describeEvent, historyNotification } from "./useNotifications";

const base = { id: 1, topic: "acquisition", entity: "work:7", at: "2026-09-25T10:00:00Z" };

test("a download reads as a sentence naming the album", () => {
  expect(
    describeEvent({ ...base, kind: "download.imported", data: { artist: "Heka", album: "Watermark", release_title: "x" } }),
  ).toBe("Imported: Heka — Watermark");
  expect(
    describeEvent({ ...base, kind: "download.failed", data: { release_title: "Heka-Watermark", detail: "Repair failed" } }),
  ).toBe("Download failed: Heka-Watermark (Repair failed)");
});

test("anything else keeps the entity and kind", () => {
  expect(describeEvent({ ...base, topic: "job", entity: "job:42", kind: "updated" })).toBe("job:42 updated");
});

test("a stored history entry reads as its live event did", () => {
  expect(
    describeEvent(historyNotification({
      id: 3, at: "2026-09-25T10:00:00Z", kind: "grabbed", work_id: 1,
      release_title: "Heka-Detisisu.Binip.1992.[FLAC]", album: "Detisisu Binip", artist: "Heka",
    })),
  ).toBe("Grabbed: Heka — Detisisu Binip");
});
