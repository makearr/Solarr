import { describe, expect, it } from "vitest";

import { isSearchShortcut } from "./searchShortcut";

const key = (overrides: Partial<Parameters<typeof isSearchShortcut>[0]>) => ({
  key: "/",
  ctrlKey: false,
  metaKey: false,
  altKey: false,
  target: { tagName: "BODY", isContentEditable: false } as unknown as EventTarget,
  ...overrides,
});

describe("isSearchShortcut", () => {
  it("takes a bare slash on the page", () => expect(isSearchShortcut(key({}))).toBe(true));
  it("leaves a slash typed into a field", () =>
    expect(isSearchShortcut(key({ target: { tagName: "INPUT" } as unknown as EventTarget }))).toBe(false));
  it("leaves a slash in an editable element", () =>
    expect(isSearchShortcut(key({ target: { tagName: "DIV", isContentEditable: true } as unknown as EventTarget }))).toBe(false));
  it("leaves a modified slash", () => expect(isSearchShortcut(key({ ctrlKey: true }))).toBe(false));
  it("ignores other keys", () => expect(isSearchShortcut(key({ key: "s" }))).toBe(false));
});
