import { MutationObserver } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, expect, test, vi } from "vitest";

import { describe as describeEvent, useNotifications } from "@/lib/useNotifications";
import { queryClient } from "./queryClient";

beforeEach(() => {
  vi.stubGlobal("EventSource", class { addEventListener() {} close() {} });
});

async function fail(options: { meta?: Record<string, unknown>; onError?: () => void } = {}) {
  const observer = new MutationObserver(queryClient, {
    mutationFn: () => Promise.reject({ title: "Conflict", detail: "The collection is in use" }),
    ...options,
  });
  await observer.mutate().catch(() => undefined);
}

test("a failing mutation that does not render its error reaches the bar", async () => {
  const { result } = renderHook(() => useNotifications());
  await act(async () => fail());
  await waitFor(() => expect(result.current.recent.map(describeEvent)).toEqual(["The collection is in use"]));
});

test("a mutation that renders its own error stays out of the bar", async () => {
  const { result } = renderHook(() => useNotifications());
  await act(async () => {
    await fail({ meta: { handlesErrors: true } });
    await fail({ onError: () => undefined });
  });
  expect(result.current.recent).toEqual([]);
});
