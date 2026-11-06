import { expect, test } from "vitest";

import type { components } from "@/api/schema";

import { eventRows, guestMembers } from "./Crossovers";

type Crossover = components["schemas"]["Crossover"];

const member = (partId: number, workId: number, number = partId, guessed = false) => ({
  part_id: partId, work_id: workId, work_title: `Series ${workId}`, media_ref: "s01:e01", title: "", monitored: true, has_file: false, number, guessed,
});

// Watch order A1, B1, A2, C1: series A shows B1 after its A1 and C1 after it too; series B shows A1 above, A2 and C1 below.
const event: Crossover = { id: 9, name: "Event", monitored: false, total_parts: 4, missing: [], gaps: [], members: [member(1, 10), member(2, 20), member(3, 10), member(4, 30)] };

test("each series shows the event's other episodes around its first one, in watch order", () => {
  const parts = (partId: number, workId: number, placement: "before" | "after") =>
    guestMembers(partId, workId, placement, [event]).map((row) => row.member.part_id);
  expect([parts(1, 10, "before"), parts(1, 10, "after")]).toEqual([[], [2, 4]]);
  expect([parts(2, 20, "before"), parts(2, 20, "after")]).toEqual([[1], [3, 4]]);
  expect(parts(3, 10, "after")).toEqual([]);
});

test("an event lists held, missing and unknown parts in part order", () => {
  const missing = { number: 2, provider: "tvdb", external_id: "33", series_title: "Sequel", season: 2, episode: 3, title: "Part Two" };
  const rows = eventRows({ members: [member(7, 10, 3, true), member(5, 10, 1)], missing: [missing], gaps: [4] });
  expect(rows.map((row) => [row.kind, row.number])).toEqual([["member", 1], ["missing", 2], ["member", 3], ["gap", 4]]);
});
