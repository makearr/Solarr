import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { act, render, waitFor } from "@testing-library/react";
import { useState } from "react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { Page } from "./useLibraryList";
import { useLibraryList } from "./useLibraryList";
import { useRouteView } from "./useRouteView";

interface Row {
  id: number;
}

const FILTER_KEYS = ["state"];

/** Every stream the hook opens, so a test can count them rather than trust that
 *  an effect ran once. */
const opened: { url: string; closed: boolean }[] = [];

class StubEventSource {
  private readonly record: { url: string; closed: boolean };

  constructor(url: string) {
    this.record = { url, closed: false };
    opened.push(this.record);
  }

  addEventListener() {}
  close() {
    this.record.closed = true;
  }
}

beforeEach(() => {
  opened.length = 0;
  vi.stubGlobal("EventSource", StubEventSource);
});

/** Renders the hook inside a real router, and hands back both the latest hook
 *  result and the router driving it. */
function mount(options: { search?: string; topics?: readonly string[]; fetchPage?: (args: { cursor?: string }) => Promise<Page<Row>> } = {}) {
  const seen: {
    list?: ReturnType<typeof useLibraryList<Row>>;
    setRowHeight?: (height: number) => void;
  } = {};
  const fetchPage = vi.fn(
    options.fetchPage ?? (async (): Promise<Page<Row>> => ({ items: [{ id: 1 }], next_cursor: undefined })),
  );

  function Screen() {
    const [rowHeight, setRowHeight] = useState(36);
    seen.setRowHeight = setRowHeight;
    seen.list = useLibraryList<Row>(
      {
        name: "rows",
        rowHeight,
        idOf: (row) => row.id,
        // Spread on purpose: a caller writing its source inline hands over a new
        // array every render, and that is the case worth holding.
        topics: options.topics ? [...options.topics] : undefined,
        fetchPage,
      },
      useRouteView(FILTER_KEYS),
    );
    return <div data-testid="rendered">{seen.list.items.length}</div>;
  }

  const rootRoute = createRootRoute();
  const listRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/list",
    component: Screen,
    validateSearch: (search: Record<string, unknown>) => search,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([listRoute]),
    history: createMemoryHistory({ initialEntries: [`/list${options.search ?? ""}`] }),
  });

  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      {/* eslint-disable-next-line @typescript-eslint/no-explicit-any */}
      <RouterProvider router={router as any} />
    </QueryClientProvider>,
  );

  // The app registers its own route tree globally, so this router's paths do not
  // typecheck against it. The cast is the seam between the two, in one place.
  const goTo = (search: Record<string, string>) =>
    (router as unknown as { navigate: (options: unknown) => Promise<void> }).navigate({
      to: "/list",
      search,
    });

  return { seen, router, goTo, fetchPage };
}

describe("the list's view state", () => {
  test("starts from the URL, so a link into a filtered view arrives filtered", async () => {
    const { seen } = mount({ search: "?state=running" });

    await waitFor(() => expect(seen.list?.query.filters.state).toBe("running"));
  });

  // The bug this test exists for: holding the query in useState reads the URL once,
  // at mount. Navigating to the same route with different search then changes
  // nothing, which is exactly what a dashboard figure linking into a filtered view
  // does (section 21.12).
  test("follows a navigation to the same route with different filters", async () => {
    const { seen, goTo } = mount({ search: "?state=running" });
    await waitFor(() => expect(seen.list?.query.filters.state).toBe("running"));

    await act(async () => {
      await goTo({ state: "failed" });
    });

    await waitFor(() => expect(seen.list?.query.filters.state).toBe("failed"));
  });

  test("a filter change reaches the URL", async () => {
    const { seen, router } = mount();
    await waitFor(() => expect(seen.list).toBeDefined());

    await act(async () => {
      seen.list?.filterBy("state", "failed");
    });

    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ state: "failed" }),
    );
  });

  // Or the selection acts on rows the user can no longer see.
  test("changing a filter clears the selection", async () => {
    const { seen } = mount();
    await waitFor(() => expect(seen.list?.items.length).toBe(1));

    await act(async () => seen.list?.toggle(1));
    await waitFor(() => expect(seen.list?.selectionIsEmpty).toBe(false));

    await act(async () => {
      seen.list?.filterBy("state", "failed");
    });
    await waitFor(() => expect(seen.list?.selectionIsEmpty).toBe(true));
  });
});

describe("the selection and paging", () => {
  // Back, forward or a link changes the view without the hook's own setQuery.
  test("a view the URL changes clears the selection", async () => {
    const { seen, goTo } = mount();
    await waitFor(() => expect(seen.list?.items.length).toBe(1));
    await act(async () => seen.list?.toggle(1));
    await waitFor(() => expect(seen.list?.selectionIsEmpty).toBe(false));

    await act(async () => {
      await goTo({ state: "failed" });
    });

    await waitFor(() => expect(seen.list?.selectionIsEmpty).toBe(true));
  });

  // Each failure re-renders the list; asking again from the effect would never stop.
  test("a page that failed is not asked for again and again", async () => {
    const { fetchPage } = mount({
      fetchPage: async ({ cursor }) => {
        if (cursor) throw new Error("the server is restarting");
        return { items: [{ id: 1 }], next_cursor: "page-2" };
      },
    });
    await waitFor(() => expect(fetchPage).toHaveBeenCalledTimes(2));
    await new Promise((resolve) => setTimeout(resolve, 200));

    expect(fetchPage).toHaveBeenCalledTimes(2);
  });
});

describe("the event stream", () => {
  // A source written inline hands over a new topics array every render. Depending
  // on that identity closes and reopens the stream on each one.
  test("is opened once, however many times the caller re-renders", async () => {
    const { seen, goTo } = mount({ topics: ["job"] });
    await waitFor(() => expect(opened.length).toBe(1));

    await act(async () => {
      await goTo({ state: "failed" });
    });
    await waitFor(() => expect(seen.list?.query.filters.state).toBe("failed"));

    expect(opened).toHaveLength(1);
    expect(opened[0]?.closed).toBe(false);
  });

  test("is not opened at all by a list with no topics", async () => {
    const { seen } = mount();
    await waitFor(() => expect(seen.list).toBeDefined());

    expect(opened).toHaveLength(0);
  });
});

describe("the virtualised layout", () => {
  // The virtualiser caches row positions until its row count changes, so a
  // resized poster wall, or a switch to the list with the same number of rows,
  // kept the old heights and drew rows on top of each other.
  test("follows a row height change that leaves the row count alone", async () => {
    const { seen } = mount();
    await waitFor(() => expect(seen.list?.items.length).toBe(1));
    expect(seen.list?.totalSize).toBe(36);

    await act(async () => seen.setRowHeight?.(240));

    await waitFor(() => expect(seen.list?.totalSize).toBe(240));
  });
});
