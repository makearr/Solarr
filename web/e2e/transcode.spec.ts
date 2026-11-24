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

test("Settings > Transcode opens with the master switch off, the hardware card and an empty file list", async ({ page }) => {
  await signIn(page);
  const summary = await page.request.get("/api/v1/transcode/summary");
  expect(summary.ok()).toBe(true);
  expect(((await summary.json()) as { enabled: boolean }).enabled).toBe(false);

  await page.goto("/settings/transcode");
  await expect(page.getByText("Transcoding", { exact: true }).first()).toBeVisible();
  await expect(page.getByText("Hardware", { exact: true })).toBeVisible();
  await expect(page.getByText("Something went wrong")).toBeHidden();

  await page.goto("/settings/transcode/files");
  await expect(page.getByText("No files match.")).toBeVisible();
});
