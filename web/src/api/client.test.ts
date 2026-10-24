// @vitest-environment jsdom
import { afterEach, expect, test, vi } from "vitest";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
});

// A proxy's empty 502 while the server restarts must read as the failure it is, at every `if (error)` and in ok().
test("a failure with no body is still an error", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 502, statusText: "Bad Gateway" })));
  // Node's Request, unlike a browser's, takes no relative URL.
  const NodeRequest = Request;
  vi.stubGlobal("Request", class extends NodeRequest {
    constructor(input: RequestInfo | URL, init?: RequestInit) {
      super(typeof input === "string" ? new URL(input, "http://solarr.test") : input, init);
    }
  });
  const { api, ok } = await import("./client");

  const { error } = await api.GET("/me");
  expect(error).toMatchObject({ status: 502, title: "Bad Gateway" });
  await expect(ok(api.GET("/me"))).rejects.toMatchObject({ status: 502 });
});
