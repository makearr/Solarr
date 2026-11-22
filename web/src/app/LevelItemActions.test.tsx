import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { ActionButtons, MonitorToggle, useItemActions } from "./LevelItemActions";

const state = { role: "user", permission: "read" };
vi.mock("@tanstack/react-router", () => ({ useParams: () => ({ categoryId: "1" }) }));
vi.mock("@/lib/useAdministrator", () => ({ useIsAdministrator: () => state.role === "admin" }));
vi.mock("@/lib/useLibrary", () => ({ useLibrary: () => ({ id: 1, permission: state.permission }) }));
vi.mock("./work/EditWork", () => ({ EditWorkButton: () => <button aria-label="Edit work" /> }));

function Card() {
  const actions = useItemActions("works", 7, true);
  return (
    <div>
      <MonitorToggle actions={actions} />
      <ActionButtons actions={actions} />
    </div>
  );
}

const shown = () =>
  ["Monitored: click to stop", "Search: send the best release", "Refresh metadata", "Edit work"].filter(
    (label) => screen.queryByLabelText(label) !== null,
  );

function mount() {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <Card />
    </QueryClientProvider>,
  );
}

describe("library card actions follow the library permission", () => {
  beforeEach(() => {
    state.role = "user";
  });
  test.each([
    ["read", []],
    ["request", []],
    ["add", ["Monitored: click to stop", "Search: send the best release", "Refresh metadata"]],
    ["manage", ["Monitored: click to stop", "Search: send the best release", "Refresh metadata", "Edit work"]],
  ])("%s", (permission, expected) => {
    state.permission = permission;
    mount();
    expect(shown()).toEqual(expected);
  });
});
