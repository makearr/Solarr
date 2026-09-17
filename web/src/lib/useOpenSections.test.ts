import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, test } from "vitest";

import { useOpenSections } from "./useOpenSections";

beforeEach(() => localStorage.clear());

describe("useOpenSections", () => {
  test("starts from the defaults on a first visit", () => {
    const { result } = renderHook(() => useOpenSections([], ["library"]));

    expect(result.current.isOpen("library")).toBe(true);
    expect(result.current.isOpen("storage")).toBe(false);
  });

  // Collapsing Library must survive a reload, or the default reopens it every time.
  test("a remembered choice wins over the defaults", () => {
    const first = renderHook(() => useOpenSections([], ["library"]));
    act(() => first.result.current.toggle("library"));
    first.unmount();

    const { result } = renderHook(() => useOpenSections([], ["library"]));
    expect(result.current.isOpen("library")).toBe(false);
  });

  test("several sections stay open together", () => {
    const { result } = renderHook(() => useOpenSections([]));
    act(() => result.current.toggle("storage"));
    act(() => result.current.toggle("settings"));

    expect(result.current.isOpen("storage")).toBe(true);
    expect(result.current.isOpen("settings")).toBe(true);
  });

  test("navigating into a branch opens it without closing the others", () => {
    let active: string[] = [];
    const { result, rerender } = renderHook(() => useOpenSections(active));
    act(() => result.current.toggle("storage"));

    active = ["settings"];
    rerender();

    expect(result.current.isOpen("storage")).toBe(true);
    expect(result.current.isOpen("settings")).toBe(true);
  });
});
