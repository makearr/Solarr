import { describe, expect, it } from "vitest";

import { curlLine, outgoingProblems } from "./Webhooks";

const good = { name: "Chat", preset: "discord", address: "https://hooks.example.org/x", events: ["imported"], adding: true };

describe("outgoingProblems", () => {
  it("accepts a complete form", () => {
    expect(outgoingProblems(good)).toEqual([]);
  });

  it("names every missing piece", () => {
    expect(outgoingProblems({ ...good, name: " ", preset: "", address: "ftp://x", events: [] })).toEqual([
      "webhooks.needName", "webhooks.needPreset", "webhooks.needAddress", "webhooks.needEvent",
    ]);
  });

  it("keeps the stored address when editing and none is typed", () => {
    expect(outgoingProblems({ ...good, address: "", adding: false })).toEqual([]);
    expect(outgoingProblems({ ...good, address: "nonsense", adding: false })).toEqual(["webhooks.needAddress"]);
  });
});

describe("curlLine", () => {
  it("carries the token in a header and the action in the body", () => {
    const line = curlLine("http://solarr.local:8080", 3, "tok", "scan");
    expect(line).toContain("Authorization: Bearer tok");
    expect(line).toContain(`{"action":"scan","library_id":1}`);
    expect(line.endsWith("http://solarr.local:8080/api/v1/hooks/3")).toBe(true);
  });
});
