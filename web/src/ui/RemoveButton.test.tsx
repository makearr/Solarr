import { fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";

import { RemoveButton } from "./RemoveButton";

const confirm = { title: "Remove this indexer?", body: "Removes the indexer and its API key." };

test("a click asks first; only confirming removes", () => {
  const onClick = vi.fn();
  render(<RemoveButton aria-label="Remove Example" confirm={confirm} onClick={onClick} />);

  fireEvent.click(screen.getByLabelText("Remove Example"));
  expect(onClick).not.toHaveBeenCalled();
  expect(screen.getByText("Removes the indexer and its API key.")).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(onClick).not.toHaveBeenCalled();

  fireEvent.click(screen.getByLabelText("Remove Example"));
  fireEvent.click(screen.getByRole("button", { name: "Remove" }));
  expect(onClick).toHaveBeenCalledTimes(1);
});

test("a caller that already asks passes null and acts at once", () => {
  const onClick = vi.fn();
  render(<RemoveButton aria-label="Remove Example" confirm={null} onClick={onClick} />);
  fireEvent.click(screen.getByLabelText("Remove Example"));
  expect(onClick).toHaveBeenCalledTimes(1);
});
