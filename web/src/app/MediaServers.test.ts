import { describe, expect, it } from "vitest";

import { usesOf } from "./MediaServers";

describe("usesOf", () => {
  it("names only what is switched on", () => {
    expect(usesOf({ as_metadata: false, push_files: true, announce: false, sync_playback: true })).toEqual(["Scan push", "Watch state"]);
  });

  it("names nothing for a connection only stored", () => {
    expect(usesOf({ as_metadata: false, push_files: false, announce: false, sync_playback: false })).toEqual([]);
  });
});
