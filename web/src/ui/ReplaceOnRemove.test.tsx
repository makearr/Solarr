import { fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";

import { ReplaceOnRemove } from "./ReplaceOnRemove";

const choices = [
  { id: 2, name: "Lossless" },
  { id: 3, name: "Any" },
];

test("the refusal is shown and the chosen replacement is passed on", () => {
  const onReplace = vi.fn();
  render(<ReplaceOnRemove name="Old" reason="3 items pinned to it" choices={choices} pending={false} onReplace={onReplace} onCancel={() => {}} />);
  expect(screen.getByText("3 items pinned to it")).toBeTruthy();
  fireEvent.change(screen.getByRole("combobox"), { target: { value: "3" } });
  fireEvent.click(screen.getByRole("button", { name: "Move them and remove Old" }));
  expect(onReplace).toHaveBeenCalledWith(3);
});

test("with nothing to move to there is no button, only the reason", () => {
  render(<ReplaceOnRemove name="Old" reason="in use" choices={[]} pending={false} onReplace={() => {}} onCancel={() => {}} />);
  expect(screen.queryByRole("button", { name: /Move them/ })).toBeNull();
  expect(screen.getByText(/no other one/)).toBeTruthy();
});
