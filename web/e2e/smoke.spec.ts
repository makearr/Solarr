import { test, expect, type Page } from "@playwright/test";

/** The credentials make-e2e's own harness expects; nothing else knows them. */
const USERNAME = "admin";
const PASSWORD = "TestPass123!TestPass";

/** Mounted read-only into the instance by the harness. */
const LIBRARY_ROOT = "/library";
const DOWNLOAD_FIXTURE = "/media/download/Goual-Zubinugi 1986-WEB-2024-ENRiCH";

/** The suite runs in order against one instance: step 2 needs step 1's account,
 *  and the import needs the library. Playwright's default is parallel files,
 *  which would race on the same server. */
test.describe.configure({ mode: "serial" });

test("a fresh install can be set up, and lands on the dashboard", async ({ page }) => {
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Set up Solarr" })).toBeVisible();
  await page.getByLabel("Username").fill(USERNAME);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: /create/i }).click();

  // The *Arr step is skippable and this install has no *Arr to connect to.
  await page.getByRole("button", { name: /skip/i }).first().click();

  await page.getByLabel("Path").fill(LIBRARY_ROOT);
  await page.getByRole("button", { name: /add/i }).first().click();

  await expect(page.getByRole("heading", { name: "Set up Solarr" })).toBeHidden();
});

// The 2026-09-19 finding: a default install could add a root folder and then had
// no way at all to create a library, because AddLibrary was gated to the advanced
// tier. M1 acceptance 1 and 8 both require this at the normal tier.
test("a library can be created at the default tier", async ({ page }) => {
  await signIn(page);
  await page.goto("/libraries");

  await expect(page.getByRole("heading", { name: "Libraries" })).toBeVisible();
  await page.getByLabel(/name/i).first().fill("Music");
  await page.getByRole("button", { name: /add librar/i }).click();

  await expect(page.getByText("Music", { exact: true }).first()).toBeVisible();
});

// What the 2026-09-13 correction was about: the tree a default install actually
// shows. Import and Unidentified were advanced until 2026-09-19, so a normal user
// could reach neither.
test("the default tier shows the whole library tree", async ({ page }) => {
  await signIn(page);
  await page.goto("/");

  const nav = page.getByRole("navigation");
  await nav.getByText("Music", { exact: true }).first().click();

  for (const entry of ["Artists", "Albums", "Tracks", "Wanted", "Import", "Unidentified"]) {
    await expect(nav.getByRole("link", { name: entry, exact: true })).toBeVisible();
  }
  // Advanced by decision D4, so their absence here is the assertion.
  for (const entry of ["Packs", "Activity"]) {
    await expect(nav.getByRole("link", { name: entry, exact: true })).toBeHidden();
  }
});

// The whole import path end to end, without placing anything until asked:
// preview holds the unit, the bulk commit places it, the revert undoes it.
test("a folder previews, commits and reverts", async ({ page }) => {
  await signIn(page);
  await page.goto("/library/1/import");

  await page.getByLabel("Source path").fill(DOWNLOAD_FIXTURE);
  await page.getByText("Preview first").click();
  await page.getByRole("button", { name: "Import", exact: true }).click();

  const held = page.getByRole("button", { name: /commit all \d+ held/i });
  await expect(held).toBeVisible({ timeout: 90_000 });

  page.once("dialog", (dialog) => dialog.accept());
  await held.click();
  await expect(page.getByText(/\d+ committed/)).toBeVisible();

  await expect(page.getByTitle("Revert this session")).toBeVisible();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByTitle("Revert this session").click();
  await expect(page.getByText("REVERTED").first()).toBeVisible({ timeout: 60_000 });
});

// The path picker and dismissal, both from the 2026-09-19 review: a folder is
// reached by browsing rather than remembering, and a finished session can be
// put away again.
test("a source path can be browsed, and a finished session dismissed", async ({ page }) => {
  await signIn(page);
  await page.goto("/library/1/import");

  const field = page.getByLabel("Source path");
  await field.fill("/media/down");
  await expect(page.getByRole("button", { name: "download", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "download", exact: true }).click();
  await expect(field).toHaveValue("/media/download/");

  const dismiss = page.getByTitle(/Dismiss this session/);
  const before = await dismiss.count();
  expect(before).toBeGreaterThan(0);
  await dismiss.first().click();
  await expect(dismiss).toHaveCount(before - 1);
});

async function signIn(page: Page) {
  await page.goto("/");
  const username = page.getByLabel("Username");
  if (!(await username.isVisible().catch(() => false))) return; // already signed in
  await username.fill(USERNAME);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: /sign in|log in/i }).click();
  await expect(username).toBeHidden();
}
