import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, test, vi } from "vitest";

import { Gauge, MetricChart } from "./SystemChart";

const get = vi.fn();
vi.mock("@/api/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/client")>()),
  api: { GET: (...args: unknown[]) => get(...args) },
  API_PREFIX: "/api/v1",
}));

function mount() {
  get.mockImplementation((_path: string, init: { params: { query: { scope: string } } }) =>
    Promise.resolve({ data: { points: [{ at: 1000, value: init.params.query.scope === "nvme" ? 10 : 30 }, { at: 1060, value: init.params.query.scope === "nvme" ? 20 : 40 }] } }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <MetricChart
        title="Calls"
        range="hour"
        format="number"
        lines={[{ label: "nvme", series: "diskgroup.calls_per_min", scope: "nvme" }, { label: "nfs", series: "diskgroup.calls_per_min", scope: "nfs" }]}
      />
    </QueryClientProvider>,
  );
}

test("a chart draws a line per series and the legend hides and shows each one", async () => {
  mount();
  const nvme = await screen.findByRole("button", { name: /nvme/ });
  await waitFor(() => expect(document.querySelectorAll("svg path")).toHaveLength(2));
  fireEvent.click(nvme);
  expect(nvme.getAttribute("aria-pressed")).toBe("false");
  expect(document.querySelectorAll("svg path")).toHaveLength(1);
  fireEvent.click(nvme);
  expect(document.querySelectorAll("svg path")).toHaveLength(2);
});

test("a gauge writes the figure in words and fills in proportion to its ceiling", () => {
  const { container } = render(<Gauge label="CPU" value={50} max={100} format="percent" />);
  expect(screen.getByRole("img", { name: "CPU 50.0%" })).toBeTruthy();
  expect(container.querySelectorAll("path")).toHaveLength(2);
  const { container: empty } = render(<Gauge label="Idle" value={0} max={100} format="percent" />);
  expect(empty.querySelectorAll("path")).toHaveLength(1);
});
