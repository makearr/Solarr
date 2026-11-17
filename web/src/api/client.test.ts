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

async function clientAnswering(status: number) {
  const NodeRequest = Request;
  vi.stubGlobal("Request", class extends NodeRequest {
    constructor(input: RequestInfo | URL, init?: RequestInit) {
      super(typeof input === "string" ? new URL(input, "http://solarr.test") : input, init);
    }
  });
  vi.stubGlobal("fetch", vi.fn(async (request: Request | string) => {
    const response = new Response(JSON.stringify({ status, title: "Unauthorized", type: "unauthorized" }), { status });
    Object.defineProperty(response, "url", { value: typeof request === "string" ? request : request.url });
    return response;
  }));
  const { api } = await import("./client");
  const { queryClient } = await import("./queryClient");
  const invalidate = vi.spyOn(queryClient, "invalidateQueries");
  return { api, invalidate };
}

// An ended session sends every screen back to sign-in, not to "request failed" panels.
test("a 401 from a screen's call asks /me again, once for a burst", async () => {
  const { api, invalidate } = await clientAnswering(401);
  await Promise.all([api.GET("/works"), api.GET("/works")]);
  expect(invalidate.mock.calls).toEqual([[{ queryKey: ["me"], exact: true }]]);
});

test("a 401 from sign-in or /me itself does not", async () => {
  const { api, invalidate } = await clientAnswering(401);
  await api.POST("/auth/login", { body: { username: "someone", password: "wrong", remember: false } });
  await api.GET("/me");
  expect(invalidate.mock.calls).toEqual([]);
});
