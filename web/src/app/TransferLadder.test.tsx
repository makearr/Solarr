import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, test, vi } from "vitest";

import { TransferLadderControl } from "./Import";

const get = vi.fn();
const patch = vi.fn();
vi.mock("@/api/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/client")>()),
  api: {
    GET: (...args: unknown[]) => get(...args),
    PATCH: (...args: unknown[]) => patch(...args),
  },
  API_PREFIX: "/api/v1",
}));

// Import.tsx reads the route's category id; this control is mounted without a
// RouterProvider, so useParams stands in as empty.
vi.mock("@tanstack/react-router", () => ({ useParams: () => ({}) }));

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <TransferLadderControl />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  get.mockReset();
  patch.mockReset();
  get.mockResolvedValue({
    data: {
      auto_commit_confidence: 0.9,
      transfer_ladder: ["reflink", "hardlink", "move", "copy", "symlink"],
    },
  });
});

test("the methods are listed in their stored order", async () => {
  mount();
  await screen.findByText("Reflink");
  for (const label of ["Reflink", "Hardlink", "Move", "Copy", "Symlink"]) {
    expect(screen.getByText(label)).toBeTruthy();
  }
});

test("no explanation is shown until the help button is clicked", async () => {
  mount();
  await screen.findByText("Reflink");

  // Nothing describing what a method does, for any method, before asking.
  expect(screen.queryByText(/Copy-on-write clone/)).toBeNull();
  expect(screen.queryByText(/second directory entry/)).toBeNull();
  expect(screen.queryByRole("dialog")).toBeNull();
});

test("one help button opens a single dialog covering every method", async () => {
  mount();
  await screen.findByText("Reflink");

  // Exactly one, not one per method.
  const help = screen.getAllByLabelText(/What do the import methods do/);
  expect(help).toHaveLength(1);

  fireEvent.click(help[0]!);
  const dialog = await screen.findByRole("dialog");
  expect(dialog).toBeTruthy();
  expect(screen.getByText(/Copy-on-write clone/)).toBeTruthy();
  expect(screen.getByText(/second directory entry/)).toBeTruthy();
});

test("the dialog closes again", async () => {
  mount();
  await screen.findByText("Reflink");

  fireEvent.click(screen.getByLabelText(/What do the import methods do/));
  await screen.findByRole("dialog");

  fireEvent.click(screen.getByText("Close"));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
});

test("reordering writes the new order", async () => {
  patch.mockResolvedValue({ data: {} });
  mount();
  await screen.findByText("Hardlink");

  fireEvent.click(screen.getByLabelText("Move Hardlink up"));
  await waitFor(() => expect(patch).toHaveBeenCalled());
  const body = patch.mock.calls[0]![1].body;
  expect(body.transfer_ladder).toEqual(["hardlink", "reflink", "move", "copy", "symlink"]);
});
