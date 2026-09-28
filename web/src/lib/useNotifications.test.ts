import { expect, test } from "vitest";

import { describe as describeEvent, historyNotification } from "./useNotifications";

const base = { id: 1, topic: "acquisition", entity: "work:7", at: "2026-09-25T10:00:00Z" };

test("a download reads as a sentence naming the album", () => {
  expect(
    describeEvent({ ...base, kind: "download.imported", data: { artist: "Heka", album: "Zanuvode", release_title: "x" } }),
  ).toBe("Imported: Heka — Zanuvode");
  expect(
    describeEvent({ ...base, kind: "download.failed", data: { release_title: "Heka-Zanuvode", detail: "Repair failed" } }),
  ).toBe("Download failed: Heka-Zanuvode (Repair failed)");
});

test("an import reads as its progress, then its outcome", () => {
  const importing = { ...base, topic: "job", entity: "import_session:1", kind: "progress" };
  expect(
    describeEvent({ ...importing, data: { phase: "walking", current_unit: "Heka/Zanuvode", units_total: 3, units_committed: 1 } }),
  ).toBe("Importing Heka/Zanuvode (1 of 3 added)");
  expect(
    describeEvent({ ...importing, data: { phase: "done", units_total: 3, units_committed: 1, units_queued: 1, units_failed: 1 } }),
  ).toBe("Import finished: 1 of 3 added, 1 waiting for review, 1 failed");
  expect(describeEvent({ ...importing, data: { phase: "walking", units_total: 0 } })).toBe("Importing…");
  expect(describeEvent({ ...importing, data: { phase: "cancelled" } })).toBe("Import cancelled");
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
