import { expect, test } from "vitest";

import { stateChip } from "./ProviderDefinitions";

test("a state chip says what an administrator must do", () => {
  expect(stateChip("needs_approval")).toEqual({ tone: "warn", label: "Needs approval" });
  expect(stateChip("active")).toEqual({ tone: "ok", label: "On" });
  expect(stateChip("error").tone).toBe("danger");
});
