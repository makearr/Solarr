import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";

import { Button } from "./Button";
import { ConfirmDialog } from "./ConfirmDialog";
import { Image } from "./Image";
import { ToolbarSearch, ToolbarSort } from "./Toolbar";

test("a busy button says so and takes no second click", () => {
  const click = vi.fn();
  render(
    <Button busy busyLabel="Searching…" onClick={click}>
      Search
    </Button>,
  );
  const button = screen.getByRole("button", { name: "Searching…" });
  fireEvent.click(button);
  expect(click).not.toHaveBeenCalled();
  expect(button.getAttribute("aria-busy")).toBe("true");
});

test("a confirm dialog names the consequence and confirms with its own label", () => {
  const confirm = vi.fn();
  const cancel = vi.fn();
  render(<ConfirmDialog title="Remove Album" consequence="The album leaves the library." confirmLabel="Remove" danger onConfirm={confirm} onCancel={cancel} />);
  expect(screen.getByText("The album leaves the library.")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Remove" }));
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect([confirm.mock.calls.length, cancel.mock.calls.length]).toEqual([1, 1]);
});

test("an image without a source shows the title's initial in its shape", () => {
  render(<Image shape="photo" title="ada lovelace" />);
  const placeholder = screen.getByLabelText("ada lovelace");
  expect([placeholder.textContent, placeholder.className.includes("rounded-full")]).toEqual(["A", true]);
});

test("the toolbar search hands on what was typed once, trimmed, after the typing pauses", () => {
  vi.useFakeTimers();
  const change = vi.fn();
  render(<ToolbarSearch value="" onChange={change} placeholder="Name starts with…" label="Filter by name" />);
  const box = screen.getByLabelText("Filter by name");
  fireEvent.change(box, { target: { value: "bl" } });
  fireEvent.change(box, { target: { value: "blue " } });
  expect(change).not.toHaveBeenCalled();
  act(() => void vi.advanceTimersByTime(250));
  vi.useRealTimers();
  expect(change.mock.calls).toEqual([["blue"]]);
});

test("the toolbar sort names its direction and turns it round", () => {
  const sort = vi.fn();
  const turn = vi.fn();
  render(<ToolbarSort sort="name" descending={false} choices={[["name", "Name"], ["works", "Albums"]]} onSort={sort} onTurn={turn} />);
  fireEvent.change(screen.getByLabelText("Sort"), { target: { value: "works" } });
  fireEvent.click(screen.getByRole("button", { name: "Ascending: turn the order round" }));
  expect([sort.mock.calls, turn.mock.calls.length]).toEqual([[["works"]], 1]);
});
