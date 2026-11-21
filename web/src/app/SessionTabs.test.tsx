import { fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";

import { SessionTabs } from "./Import";

test("the attention tab is selected by default and the other tab asks for everything", () => {
  const onChange = vi.fn();
  render(<SessionTabs attention onChange={onChange} />);

  expect(screen.getByRole("tab", { name: "Needs attention" }).getAttribute("aria-selected")).toBe("true");
  expect(screen.getByRole("tab", { name: "All" }).getAttribute("aria-selected")).toBe("false");

  fireEvent.click(screen.getByRole("tab", { name: "All" }));
  expect(onChange).toHaveBeenCalledWith(false);
});

test("on the all tab the attention tab asks to go back", () => {
  const onChange = vi.fn();
  render(<SessionTabs attention={false} onChange={onChange} />);

  expect(screen.getByRole("tab", { name: "All" }).getAttribute("aria-selected")).toBe("true");
  fireEvent.click(screen.getByRole("tab", { name: "Needs attention" }));
  expect(onChange).toHaveBeenCalledWith(true);
});
