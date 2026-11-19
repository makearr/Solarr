import { describe, expect, it } from "vitest";

import { passwordReady } from "./NewPassword";

describe("passwordReady", () => {
  it.each([
    ["", "", false],
    ["short", "short", false],
    ["longenough", "longenougx", false],
    ["longenough", "longenough", true],
    ["åäöüåäöü", "åäöüåäöü", true],
    ["åäöüåäö", "åäöüåäö", false],
  ])("%j and %j", (password, repeat, want) => {
    expect(passwordReady(password, repeat)).toBe(want);
  });
});
