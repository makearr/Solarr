import { describe, expect, it } from "vitest";

import { joinCredentials } from "./downloadCredentials";

describe("joinCredentials", () => {
  it("joins a user and password, keeping colons in the password", () => {
    expect(joinCredentials(" me ", "se:cret")).toBe("me:se:cret");
  });
  it("is empty when both are", () => {
    expect(joinCredentials("", "")).toBe("");
  });
});
