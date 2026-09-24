import { describe, expect, it } from "vitest";

import { isAdministratorPath } from "./useAdministrator";

describe("isAdministratorPath", () => {
  it.each([
    ["/settings/network", true],
    ["/settings/user", false],
    ["/storage/roots", true],
    ["/connections/indexers", true],
    ["/library/1/config/naming", true],
    ["/library/1/import", true],
    ["/library/1/albums", false],
    ["/library/1/wanted", false],
    ["/calendar", false],
  ])("%s -> %s", (path, want) => {
    expect(isAdministratorPath(path)).toBe(want);
  });
});
