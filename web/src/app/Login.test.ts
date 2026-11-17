import { expect, test } from "vitest";

import { signInFailure } from "./Login";

test("only a refused password is called incorrect", () => {
  expect(signInFailure({ status: 401, type: "unauthorized" })).toBe("The username or password is incorrect.");
  expect(signInFailure({ status: 429, type: "too-many-requests" })).toMatch(/^Too many failed sign-ins/);
  expect(signInFailure({ status: 400, type: "bad-request" })).toMatch(/disagree about how to send the password/);
  for (const error of [{ status: 500 }, { status: 502, title: "Bad Gateway" }, new TypeError("network"), undefined]) {
    expect(signInFailure(error)).toBe("The server could not be reached or had an error; the password was not checked.");
  }
});
