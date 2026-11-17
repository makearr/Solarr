import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";

import { ProfileForm } from "./QualityProfileEditor";

test("leaving the JSON switch with invalid JSON keeps the mode and says why", () => {
  vi.stubGlobal("fetch", vi.fn(() => new Promise(() => undefined)));
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ProfileForm onDone={() => undefined} onCancel={() => undefined} />
    </QueryClientProvider>,
  );

  const toggle = screen.getByLabelText("Edit the rules as JSON") as HTMLInputElement;
  fireEvent.click(toggle);
  expect(toggle.checked).toBe(true);

  fireEvent.change(screen.getByLabelText(/^Rules \(JSON\)/), { target: { value: "{bad" } });
  fireEvent.click(toggle);

  expect(toggle.checked).toBe(true);
  expect(screen.getByRole("alert").textContent).toMatch(/^These rules are not valid JSON/);

  fireEvent.change(screen.getByLabelText(/^Rules \(JSON\)/), { target: { value: "{}" } });
  expect(screen.queryByRole("alert")).toBeNull();
  fireEvent.click(toggle);
  expect(toggle.checked).toBe(false);
});
