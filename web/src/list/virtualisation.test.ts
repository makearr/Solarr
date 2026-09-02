import { describe, expect, test } from "vitest";
import { Virtualizer } from "@tanstack/virtual-core";

// 500k rows is the design point (ADR-22, ADR-28), and virtualisation is mandatory in both the
// table and the grid (section 21.2).
const ROWS = 500_000;
const ROW_HEIGHT = 36;
const VIEWPORT = 800;

function windowAt(scrollTop: number, count = ROWS) {
  const virtualizer = new Virtualizer<Element, Element>({
    count,
    estimateSize: () => ROW_HEIGHT,
    overscan: 5,
    // A stand-in scroll element: nothing is measured from it, because the
    // rect and offset are supplied directly below.
    getScrollElement: () => ({}) as Element,
    observeElementRect: (_instance, report) => {
      report({ width: 1200, height: VIEWPORT });
      return () => {};
    },
    observeElementOffset: (_instance, report) => {
      report(scrollTop, false);
      return () => {};
    },
    scrollToFn: () => {},
  });

  virtualizer._willUpdate();
  return virtualizer;
}

describe("a 500k-row list", () => {
  test("materialises a viewport's worth of rows, not the collection", () => {
    const items = windowAt(0).getVirtualItems();

    // A viewport of 800px at 36px per row is ~23 rows, plus overscan.
    expect(items.length).toBeGreaterThan(0);
    expect(items.length).toBeLessThan(50);
    expect(items[0]?.index).toBe(0);
  });

  test("the window follows the scroll position rather than growing", () => {
    const near = windowAt(0).getVirtualItems();
    const far = windowAt(ROW_HEIGHT * 400_000).getVirtualItems();

    expect(far.length).toBeLessThan(50);
    // Deep into the list the window has moved, not extended.
    expect(far[0]?.index).toBeGreaterThan(399_000);
    // Not equal to the window at the top: there, overscan only extends downwards because there
    // is nothing above row 0.
    expect(far.length).toBeLessThanOrEqual(near.length + 2 * 5);
  });

  test("the rendered count does not grow with the collection", () => {
    // The regression this catches: something that renders every row still
    // "works" on a small library and falls over on a real one.
    const thousand = windowAt(0, 1_000).getVirtualItems().length;
    const halfMillion = windowAt(0, ROWS).getVirtualItems().length;

    expect(halfMillion).toBe(thousand);
  });

  test("the scrollable height still spans the whole collection", () => {
    // Windowing must not cost a truthful scrollbar: the thumb has to reflect
    // 500k rows or the list becomes impossible to navigate.
    expect(windowAt(0).getTotalSize()).toBe(ROWS * ROW_HEIGHT);
  });
});
