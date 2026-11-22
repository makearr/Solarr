import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";

import { RunNotes } from "./ArrImport";

test("the notes an import left show as a list under their own name", () => {
  render(<RunNotes notes={["Indexers added switched off, enter their API keys: Alpha", "Box: lidarr uses category \"music\""]} />);
  const list = screen.getByRole("list", { name: "To do after this import" });
  expect(list.querySelectorAll("li")).toHaveLength(2);
  expect(screen.getByText("Indexers added switched off, enter their API keys: Alpha")).toBeTruthy();
});
