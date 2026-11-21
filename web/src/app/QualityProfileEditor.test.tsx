import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";

import { ProfileForm } from "./QualityProfileEditor";

const fetchMock = vi.hoisted(() => vi.fn());

// Node's Request takes no relative URL, so the editor gets a client with an absolute base and this test's fetch.
vi.mock("@/api/client", async () => {
  const { default: createClient } = await import("openapi-fetch");
  return {
    api: createClient({ baseUrl: "http://solarr.test/api/v1", fetch: fetchMock }),
    ok: async <T,>(call: Promise<{ data?: T }>) => (await call).data as T,
  };
});

test("leaving the JSON switch with invalid JSON keeps the mode and says why", () => {
  vi.stubGlobal("fetch", vi.fn(() => new Promise(() => undefined)));
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ProfileForm onDone={() => undefined} onCancel={() => undefined} />
    </QueryClientProvider>,
  );

  const toggle = screen.getByLabelText("Edit the rules as JSON") as HTMLInputElement;
  fireEvent.click(toggle);
  expect(toggle.checked).toBe(true);

  fireEvent.change(screen.getByLabelText(/^Rules \(JSON\)/), { target: { value: "{bad" } });
  fireEvent.click(toggle);

  expect(toggle.checked).toBe(true);
  expect(screen.getByRole("alert").textContent).toMatch(/^These rules are not valid JSON/);

  fireEvent.change(screen.getByLabelText(/^Rules \(JSON\)/), { target: { value: "{}" } });
  expect(screen.queryByRole("alert")).toBeNull();
  fireEvent.click(toggle);
  expect(toggle.checked).toBe(false);
});

test("Check lists the mistakes the server names and saves nothing", async () => {
  const problems = [{ path: "rules", value: "no_such_key", expectation: "a known key" }];
  fetchMock.mockImplementation((request: Request) =>
    Promise.resolve(
      new Response(JSON.stringify(request.url.endsWith("/qualityprofiles/validate") ? { problems } : []), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ProfileForm onDone={() => undefined} onCancel={() => undefined} />
    </QueryClientProvider>,
  );

  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Typo" } });
  fireEvent.click(screen.getByRole("button", { name: "Check" }));

  expect(await screen.findByText(/a known key/)).toBeTruthy();
  const writes = fetchMock.mock.calls.map((call) => call[0] as Request).filter((request) => request.method !== "GET");
  expect(writes.map((request) => request.url)).toEqual(["http://solarr.test/api/v1/qualityprofiles/validate"]);
});
