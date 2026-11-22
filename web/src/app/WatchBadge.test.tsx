import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";

import { WatchBadge } from "./WatchBadge";

test("a watched work shows a check, an administrator also sees how many", () => {
  render(<WatchBadge watch={{ status: "yes", watched_by: 2, of: 3 }} />);
  expect(screen.getByRole("img", { name: "Watched by 2 of 3" })).toBeTruthy();
});

test("a work played in part shows it is in progress, and one never played shows nothing", () => {
  const { container, rerender } = render(<WatchBadge watch={{ status: "in_progress" }} />);
  expect(screen.getByRole("img", { name: "In progress" })).toBeTruthy();
  rerender(<WatchBadge watch={undefined} />);
  expect(container.textContent).toBe("");
});
