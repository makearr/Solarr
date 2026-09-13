import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { SearchBox } from "./SearchBox";

const get = vi.fn();
vi.mock("@/api/client", () => ({
  api: { GET: (...args: unknown[]) => get(...args) },
  API_PREFIX: "/api/v1",
}));

// A result row is a real router Link now; the box is mounted here without a
// RouterProvider, so the link renders as a plain anchor with the path the
// router would have built.
vi.mock("@tanstack/react-router", () => ({
  Link: ({
    to,
    params,
    children,
    ...rest
  }: {
    to: string;
    params?: Record<string, string>;
    children: React.ReactNode;
  }) => {
    const href = Object.entries(params ?? {}).reduce(
      (path, [key, value]) => path.replace(`$${key}`, value),
      to,
    );
    return (
      <a href={href} {...rest}>
        {children}
      </a>
    );
  },
}));

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <SearchBox />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  get.mockReset();
  get.mockResolvedValue({ data: [], error: undefined });
});

test("a query below the 2-character floor never fires a request", async () => {
  mount();
  const input = screen.getByPlaceholderText("Search the library…");

  fireEvent.focus(input);
  fireEvent.change(input, { target: { value: "a" } });

  // Long enough to cross the 250ms debounce, so this is a real absence, not a timing gap.
  await new Promise((resolve) => setTimeout(resolve, 400));
  expect(get).not.toHaveBeenCalled();
});

test("a 2-character query debounces before firing, and shows results", async () => {
  get.mockResolvedValue({
    data: [
      {
        work: {
          id: 1, media_type: "music", title: "KI Pecezinu", sort_title: "ki pecezinu",
          year: 1997, added_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
        },
        score: 0.1,
      },
    ],
    error: undefined,
  });
  mount();
  const input = screen.getByPlaceholderText("Search the library…");

  fireEvent.focus(input);
  fireEvent.change(input, { target: { value: "ok" } });

  // Not immediate: the debounce must have actually delayed the call.
  expect(get).not.toHaveBeenCalled();

  await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
  expect(get).toHaveBeenCalledWith("/search", {
    params: { query: { q: "ok", limit: 20 } },
  });

  await screen.findByText("KI Pecezinu");
});

test("escape clears the query and closes the dropdown", async () => {
  get.mockResolvedValue({
    data: [
      {
        work: {
          id: 1, media_type: "music", title: "KI Pecezinu", sort_title: "ki pecezinu",
          year: 1997, added_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
        },
        score: 0.1,
      },
    ],
    error: undefined,
  });
  mount();
  const input = screen.getByPlaceholderText("Search the library…") as HTMLInputElement;

  fireEvent.focus(input);
  fireEvent.change(input, { target: { value: "ok" } });
  await screen.findByText("KI Pecezinu");

  fireEvent.keyDown(input, { key: "Escape" });
  expect(input.value).toBe("");
  await waitFor(() => expect(screen.queryByText("KI Pecezinu")).toBeNull());
});

test("blur closes the dropdown", async () => {
  get.mockResolvedValue({
    data: [
      {
        work: {
          id: 1, media_type: "music", title: "KI Pecezinu", sort_title: "ki pecezinu",
          year: 1997, added_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
        },
        score: 0.1,
      },
    ],
    error: undefined,
  });
  mount();
  const input = screen.getByPlaceholderText("Search the library…");

  fireEvent.focus(input);
  fireEvent.change(input, { target: { value: "ok" } });
  await screen.findByText("KI Pecezinu");

  // The close is deferred so a click on a result lands before the list
  // unmounts -- still open on the tick after blur, gone shortly after.
  fireEvent.blur(input);
  expect(screen.queryByText("KI Pecezinu")).not.toBeNull();
  await waitFor(() => expect(screen.queryByText("KI Pecezinu")).toBeNull());
});

test("a result links to its own work", async () => {
  get.mockResolvedValue({
    data: [
      {
        work: {
          id: 42, media_type: "music", title: "KI Pecezinu", sort_title: "ki pecezinu",
          year: 1997, added_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
        },
        score: 0.1,
      },
    ],
    error: undefined,
  });
  mount();
  const input = screen.getByPlaceholderText("Search the library\u2026");

  fireEvent.focus(input);
  fireEvent.change(input, { target: { value: "ok" } });
  await screen.findByText("KI Pecezinu");

  expect(screen.getByRole("link").getAttribute("href")).toBe("/works/42");
});

describe("no matches", () => {
  test("renders an empty state rather than an error", async () => {
    mount();
    const input = screen.getByPlaceholderText("Search the library…");

    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "nothing" } });

    await screen.findByText("No matches.");
  });
});
