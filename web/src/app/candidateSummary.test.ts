import { expect, test } from "vitest";

import type { components } from "@/api/schema";

import { candidateSummary, describeDeferral } from "./candidateSummary";

type Decision = components["schemas"]["CandidateDecision"];

const decision = (over: Partial<Decision>): Decision =>
  ({ id: 1, title: "A", outcome: "REJECT", blocklisted: false, rejections: [], deferrals: [], components: [], ...over }) as unknown as Decision;
const rejection = (field: string) => ({ rule: "r", field, wanted: "x", got: "y" });

test("nothing found says nothing", () => {
  expect(candidateSummary([])).toBe("");
});

test("an all-rejected search names the commonest reasons", () => {
  expect(
    candidateSummary([
      decision({ rejections: [rejection("format")] }),
      decision({ rejections: [rejection("format"), rejection("size")] }),
      decision({ rejections: [rejection("size")] }),
      decision({ rejections: [rejection("year")] }),
    ]),
  ).toBe("4 releases found; none acceptable; 4 rejected (format ×2, size ×2, year ×1).");
});

test("an acceptable release is named unless it is blocklisted", () => {
  expect(candidateSummary([decision({ outcome: "ACCEPT", title: "Best" }), decision({ rejections: [rejection("size")] })])).toBe(
    "2 releases found; 1 acceptable, the best is Best; 1 rejected (size ×1).",
  );
  expect(candidateSummary([decision({ outcome: "ACCEPT", blocklisted: true })])).toBe("1 release found.");
});

test("deferred releases give their reasons", () => {
  expect(candidateSummary([decision({ outcome: "DEFER", deferrals: [{ reason: "too new" }] as Decision["deferrals"] })])).toBe(
    "1 release found; 1 deferred (too new ×1).",
  );
});

test("a deferral says what it waits for and until when", () => {
  const now = Date.parse("2026-11-03T12:00:00Z");
  expect(describeDeferral({ reason: "protocol-delay", retry_after: "2026-11-03T11:00:00Z" }, now)).toBe(
    "Waiting for the preferred protocol to have its turn. Acted on at the next search.",
  );
  expect(describeDeferral({ reason: "unmeasured:bitrate" }, now)).toBe("The bitrate of this release is not measured yet.");
  expect(describeDeferral({ reason: "protocol-delay", retry_after: "2026-11-03T13:00:00Z" }, now)).toContain("Acted on from ");
});
