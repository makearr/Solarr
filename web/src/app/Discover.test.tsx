import { fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";

import { RecommendationCard } from "./Discover";

const item = {
  id: 7, category_id: 1, kind: "movie", provider: "tmdb", external_id: "42", title: "Example Film", subtitle: "", overview: "", poster_url: "",
  year: 2020, rating: 8.25, votes: 1200, genres: ["Drama"], types: ["trending"],
};

test("a person who may add gets a working Add; one who may not gets it disabled but still sees the card", () => {
  const onAdd = vi.fn();
  const { rerender } = render(<RecommendationCard item={item} canAdd onAdd={onAdd} onDismiss={() => {}} />);
  expect(screen.getByText("Example Film")).toBeTruthy();
  expect(screen.getByText("2020 · 8.3 (1200)")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Add" }));
  expect(onAdd).toHaveBeenCalledTimes(1);

  rerender(<RecommendationCard item={item} canAdd={false} onAdd={onAdd} onDismiss={() => {}} />);
  expect((screen.getByRole("button", { name: "Add" }) as HTMLButtonElement).disabled).toBe(true);
});

test("Not interested hands the card back to be removed", () => {
  const onDismiss = vi.fn();
  render(<RecommendationCard item={item} canAdd onAdd={() => {}} onDismiss={onDismiss} />);
  fireEvent.click(screen.getByRole("button", { name: "Not interested" }));
  expect(onDismiss).toHaveBeenCalledTimes(1);
});
