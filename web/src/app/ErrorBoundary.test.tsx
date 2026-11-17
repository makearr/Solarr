import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";

import { ErrorBoundary, reportText } from "./ErrorBoundary";

function Broken(): never {
  throw new Error("calendar date is malformed");
}

test("a child that throws shows the fallback and its sibling stays", () => {
  vi.spyOn(console, "error").mockImplementation(() => undefined);
  render(
    <QueryClientProvider client={new QueryClient()}>
      <nav>navigation</nav>
      <ErrorBoundary name="The status bar">
        <Broken />
      </ErrorBoundary>
    </QueryClientProvider>,
  );
  expect(screen.getByText("navigation")).toBeTruthy();
  expect(screen.getByText("The status bar failed")).toBeTruthy();
  expect(screen.getByText("calendar date is malformed")).toBeTruthy();
});

test("the report names the build, the place and the failure", () => {
  expect(reportText(new Error("boom"), "2026.11.1.1", "/calendar")).toContain("Solarr 2026.11.1.1\nAt /calendar\nError: boom");
});
