import { test, expect, type Page } from "@playwright/test";

const USERNAME = "admin";
const PASSWORD = "TestPass123!TestPass";

async function signIn(page: Page) {
  await page.goto("/");
  const username = page.getByLabel("Username");
  if (!(await username.isVisible().catch(() => false))) return;
  await username.fill(USERNAME);
  await page.getByLabel("Password", { exact: true }).fill(PASSWORD);
  await page.getByRole("button", { name: /sign in|log in/i }).click();
  await expect(username).toBeHidden();
}

test("the Watched page of a library explains an empty list when no connection reads watch state", async ({ page }) => {
  await signIn(page);
  const response = await page.request.get("/api/v1/libraries");
  const libraries = (await response.json()) as { id: number; watched?: number }[];
  expect(libraries.length).toBeGreaterThan(0);
  // No media server syncs watch state in this instance, so the counter was never written.
  expect(libraries[0].watched).toBeUndefined();

  await page.goto(`/library/${libraries[0].id}/watched`);
  await expect(page.getByRole("heading", { name: "Watched" })).toBeVisible();
  await expect(page.getByText(/Nothing here: no work has been played/)).toBeVisible();
  await expect(page.getByText("The list could not be read.")).toBeHidden();
});
