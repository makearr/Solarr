import { fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";

import { RecommendationCard, searchHidden, searchLibrary } from "./Discover";

const item = {
  id: 7, category_id: 1, kind: "movie", provider: "tmdb", external_id: "42", title: "Example Film", subtitle: "", overview: "", poster_url: "",
  year: 2020, rating: 8.25, votes: 1200, genres: ["Drama"], types: ["trending"],
};

test("a person who may add gets a working Add; one who may not gets it disabled but still sees the card", () => {
  const onAdd = vi.fn();
  const { rerender } = render(<RecommendationCard item={item} libraryName="Films" canAdd onAdd={onAdd} onDismiss={() => {}} />);
  expect(screen.getByText("Example Film")).toBeTruthy();
  expect(screen.getByText("2020 · 8.3 (1200)")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Add" }));
  expect(onAdd).toHaveBeenCalledTimes(1);

  rerender(<RecommendationCard item={item} libraryName="Films" canAdd={false} onAdd={onAdd} onDismiss={() => {}} />);
  expect((screen.getByRole("button", { name: "Add" }) as HTMLButtonElement).disabled).toBe(true);
});

test("Not interested hands the card back to be removed", () => {
  const onDismiss = vi.fn();
  render(<RecommendationCard item={item} libraryName="Films" canAdd onAdd={() => {}} onDismiss={onDismiss} />);
  fireEvent.click(screen.getByRole("button", { name: "Not interested" }));
  expect(onDismiss).toHaveBeenCalledTimes(1);
});

test("a held work offers Monitor, and a request-only person sees Request instead of Add", () => {
  const onMonitor = vi.fn();
  const held = { ...item, library_id: 5 };
  const { rerender } = render(<RecommendationCard item={held} libraryName="Films" canAdd onAdd={() => {}} onMonitor={onMonitor} onDismiss={() => {}} />);
  fireEvent.click(screen.getByRole("button", { name: "Monitor" }));
  expect(onMonitor).toHaveBeenCalledTimes(1);

  rerender(<RecommendationCard item={item} libraryName="Films" canAdd requestOnly onAdd={() => {}} onDismiss={() => {}} />);
  expect(screen.getByRole("button", { name: "Request" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Add" })).toBeNull();
});

test("a library and the Hidden view are read the same from a plain link and from the router's own text form", () => {
  expect(searchLibrary({ library: 2 })).toBe("2");
  expect(searchLibrary({ library: "2" })).toBe("2");
  expect(searchLibrary({})).toBeUndefined();
  expect(searchHidden({ hidden: 1 })).toBe(true);
  expect(searchHidden({ hidden: "1" })).toBe(true);
  expect(searchHidden({ hidden: true })).toBe(true);
  expect(searchHidden({})).toBe(false);
  expect(searchHidden({ hidden: 0 })).toBe(false);
});

test("every card names the library it would be added to", () => {
  render(<RecommendationCard item={item} libraryName="Films" canAdd onAdd={() => {}} onDismiss={() => {}} />);
  expect(screen.getByText("Adds to Films")).toBeTruthy();
});

test("a recommendation without a poster shows its initial", () => {
  render(<RecommendationCard item={item} libraryName="Films" canAdd onAdd={() => {}} onDismiss={() => {}} />);
  expect(screen.getByLabelText("Example Film").textContent).toBe("E");
});
