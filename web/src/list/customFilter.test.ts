import { describe as suite, expect, test } from "vitest";

import { holds, parseWhere, passes, toWhere, whereWithTitle, type FilterField } from "./customFilter";

const fields: FilterField[] = [
  { key: "title", label: "Title", kind: "text" },
  { key: "year", label: "Year", kind: "number" },
  { key: "monitored", label: "Monitored", kind: "bool" },
  { key: "status", label: "Status", kind: "enum", choices: [["ended", "Ended"], ["continuing", "Continuing"]] },
];

suite("custom filters", () => {
  test("round-trips through the where parameter and ignores what is unreadable", () => {
    const conditions = [{ field: "year", op: "gt", value: "2010" }];
    expect(parseWhere(toWhere(conditions))).toEqual(conditions);
    expect(parseWhere("not json")).toEqual([]);
    expect(parseWhere('{"a":1}')).toEqual([]);
    expect(toWhere([])).toBe("");
  });

  test("the search box adds a title condition to the saved filter and nothing when empty", () => {
    const saved = toWhere([{ field: "year", op: "gt", value: "2010" }]);
    expect([parseWhere(whereWithTitle(saved, "blue")), whereWithTitle(saved, ""), whereWithTitle("", ""), parseWhere(whereWithTitle(undefined, "blue"))]).toEqual([
      [{ field: "year", op: "gt", value: "2010" }, { field: "title", op: "contains", value: "blue" }],
      saved,
      undefined,
      [{ field: "title", op: "contains", value: "blue" }],
    ]);
  });

  test("text conditions compare case-insensitively against any of several values", () => {
    expect(holds({ field: "genre", op: "contains", value: "DRA" }, "text", ["Comedy", "Drama"])).toBe(true);
    expect(holds({ field: "genre", op: "not_contains", value: "dra" }, "text", ["Comedy", "Drama"])).toBe(false);
    expect(holds({ field: "genre", op: "is", value: "comedy" }, "text", ["Comedy"])).toBe(true);
  });

  test("numbers, booleans and enums", () => {
    expect(holds({ field: "year", op: "ge", value: "2010" }, "number", [2010])).toBe(true);
    expect(holds({ field: "year", op: "lt", value: "2010" }, "number", [2010])).toBe(false);
    expect(holds({ field: "monitored", op: "is", value: "false" }, "bool", [false])).toBe(true);
    expect(holds({ field: "status", op: "is_not", value: "ended" }, "enum", ["continuing"])).toBe(true);
  });

  test("an item must pass every condition", () => {
    const read = (field: string) => (field === "title" ? ["Hutepagpo"] : field === "year" ? [2022] : []);
    const both = [{ field: "title", op: "contains", value: "hutep" }, { field: "year", op: "gt", value: "2020" }];
    expect(passes(both, fields, read)).toBe(true);
    expect(passes([...both, { field: "year", op: "lt", value: "2000" }], fields, read)).toBe(false);
  });
});
