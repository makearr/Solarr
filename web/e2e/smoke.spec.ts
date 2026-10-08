import { test, expect, type Page } from "@playwright/test";

/** The credentials make-e2e's own harness expects; nothing else knows them. */
const USERNAME = "admin";
const PASSWORD = "TestPass123!TestPass";

/** Mounted read-only into the instance by the harness. */
const LIBRARY_ROOT = "/library";
/** The user's test library, mounted read-only by the harness. */
const MOUNTED_MUSIC = "/media/music";
// A real release folder, named in Makefile.local (E2E_IMPORT_FOLDER) so no library name is in git.
const DOWNLOAD_FIXTURE = process.env.E2E_IMPORT_FIXTURE ?? "";

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

  // TV, not Music: the wizard has already made a Music library, so adding
  // one here would be refused as a duplicate and a Music assertion would
  // pass on the wizard's own library whatever this page did. The name is
  // left blank on purpose -- a library with no name of its own is named
  // after its media type (2026-09-19).
  await page.getByLabel(/media type/i).first().selectOption("tv");
  await page.getByRole("button", { name: /add librar/i }).click();

  // Scoped to the tree: the media-type picker also holds the word TV.
  await expect(page.getByRole("navigation").getByText("TV", { exact: true })).toBeVisible();
});

// What the 2026-09-13 correction was about: the tree a default install actually
// shows. Import and Unidentified were advanced until 2026-09-19, so a normal user
// could reach neither.
test("the default tier shows the whole library tree", async ({ page }) => {
  await signIn(page);
  await page.goto("/");

  const nav = page.getByRole("navigation");
  await nav.getByText("Music", { exact: true }).first().click();

  // After its levels a library has Wanted, Insights, Files and Settings; their pages are tabs (2026-10-04).
  for (const entry of ["Artists", "Albums", "Tracks", "Labels", "Wanted", "Insights", "Files"]) {
    await expect(nav.getByRole("link", { name: entry, exact: true })).toBeVisible();
  }
  await expect(nav.getByRole("link", { name: "Settings", exact: true })).toHaveCount(1);

  // Unidentified is what Files opens on at the default tier; Packs is expert only and Import advanced.
  await nav.getByRole("link", { name: "Files", exact: true }).click();
  await expect(page.getByRole("heading", { name: /unidentified/i }).first()).toBeVisible();
  for (const entry of ["Packs", "Import"]) {
    await expect(page.getByRole("link", { name: entry, exact: true })).toBeHidden();
  }
});

// The whole import path end to end, without placing anything until asked:
// preview holds the unit, the bulk commit places it, the revert undoes it.
test("a folder previews, commits and reverts", async ({ page }) => {
  await signIn(page);
  await page.goto("/library/1/import");

  await page.getByLabel("Source path").fill(DOWNLOAD_FIXTURE);
  // The path picker's list covers the options below it until it is closed.
  await page.keyboard.press("Escape");
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

/** Images in the page that have really loaded, not merely been requested. The page is reloaded first: a picture
 *  that was missing when the wall rendered stays a placeholder until the next visit. */
async function loadedImages(page: Page): Promise<number> {
  await page.reload();
  await page.waitForTimeout(3_000);
  return page
    .locator("main img")
    .evaluateAll((images) => images.filter((image) => (image as HTMLImageElement).complete && (image as HTMLImageElement).naturalWidth > 0).length);
}

// The 2026-09-13 finding, kept from happening again: artist images and album covers were declared done while
// none had ever been fetched. This scans the mounted library (read-only, ~1,100 files) with the real
// metadata providers and waits for the walls to show real pictures.
test("a scan fills the artist wall with photos and the album wall with covers", async ({ page }) => {
  test.setTimeout(10 * 60_000);
  await signIn(page);

  await page.goto("/storage/roots");
  await page.getByLabel("Path").fill(MOUNTED_MUSIC);
  // Named explicitly: the form's default is the first library listed, which is not always Music.
  const form = page.locator("form").filter({ has: page.getByRole("button", { name: /add root folder/i }) });
  await form.getByRole("combobox").selectOption({ label: "Music (music)" });
  await form.getByRole("button", { name: /add root folder/i }).click();
  await expect(page.getByText(MOUNTED_MUSIC).first()).toBeVisible();

  // The scan starts on attach; the Albums count in the tree says how many it has found.
  await expect.poll(async () => albumCount(page), { timeout: 4 * 60_000, intervals: [3_000] }).toBeGreaterThan(0);
  await page.goto("/library/1/artists");

  // Photos come from the providers in the background, a few a minute at MusicBrainz's pace.
  await expect.poll(async () => loadedImages(page), { timeout: 6 * 60_000, intervals: [5_000] }).toBeGreaterThan(2);
  await page.screenshot({ path: "/e2e-out/artists-wall.png" });

  // One person page for every library: the artist's own section lists the discography by release type.
  await page.locator('a[href^="/library/1/people/"]').first().click();
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await expect(page.getByText(/^(Albums|EPs|Singles|Not yet classified) \(\d+\)$/).first()).toBeVisible();
  await page.screenshot({ path: "/e2e-out/artist-page.png", fullPage: true });

  // The global People list holds the same people, from every library the viewer may read.
  await page.goto("/people");
  await expect.poll(async () => loadedImages(page), { timeout: 60_000, intervals: [3_000] }).toBeGreaterThan(2);
  await page.screenshot({ path: "/e2e-out/people.png" });

  await page.goto("/library/1/albums");
  await expect.poll(async () => loadedImages(page), { timeout: 6 * 60_000, intervals: [5_000] }).toBeGreaterThan(2);
  await page.screenshot({ path: "/e2e-out/albums-wall.png" });
});

// 2026-10-04: the bin shows what it holds and can be emptied by hand.
test("the recycle bin lists its contents and offers to empty it", async ({ page }) => {
  await signIn(page);
  await page.goto("/storage/recycle-bin");
  await expect(page.getByRole("heading", { name: "Recycle bin" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Empty recycle bin" })).toBeVisible();
  await page.screenshot({ path: "/e2e-out/recycle-bin.png" });
});

// 2026-10-04: target qualities are named and shared, and language profiles sit beneath them.
test("a target quality can be made under quality profiles, and language profiles are there too", async ({ page }) => {
  await signIn(page);
  await page.goto("/quality-profiles");
  await expect(page.getByRole("heading", { name: "Target qualities" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Language profiles" })).toBeVisible();

  await page.getByRole("button", { name: "New target" }).click();
  await page.getByLabel("Name").first().fill("E2E 1080p");
  await page.getByLabel("For").selectOption("tv");
  for (const group of ["Container", "Video", "Audio"]) {
    await expect(page.getByText(group, { exact: true }).first()).toBeVisible();
  }
  await page.screenshot({ path: "/e2e-out/target-form.png" });
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByText("E2E 1080p")).toBeVisible();
  await page.getByRole("button", { name: "Remove target E2E 1080p" }).click();
  await expect(page.getByText("E2E 1080p")).toBeHidden();
});

// 2026-10-04: Sonarr's custom filter, on the music library's Albums level.
test("a custom filter can be built on the Albums level", async ({ page }) => {
  await signIn(page);
  await page.goto("/library/1/albums");
  await page.getByRole("button", { name: "Filter" }).click();
  await page.getByRole("menuitem", { name: "Custom filter…" }).click();
  await page.getByLabel("Field", { exact: true }).selectOption("year");
  await page.getByLabel("Condition", { exact: true }).selectOption("gt");
  await page.getByLabel("Value", { exact: true }).fill("1900");
  await page.getByRole("button", { name: "Apply" }).click();
  await expect(page).toHaveURL(/where=/);
  await expect(page.getByRole("button", { name: "Custom filter" })).toBeVisible();
  await page.screenshot({ path: "/e2e-out/albums-filter.png" });

  // Named, a filter is kept in the menu and applies again from there.
  await page.getByRole("button", { name: "Custom filter" }).click();
  await page.getByRole("menuitem", { name: "Save this filter…" }).click();
  await page.getByLabel("Name", { exact: true }).fill("Newer albums");
  await page.getByRole("button", { name: "Save and apply" }).click();
  await expect(page.getByRole("button", { name: "Newer albums" })).toBeVisible();
  await page.getByRole("button", { name: "Newer albums" }).click();
  await page.getByRole("menuitem", { name: "Everything" }).click();
  await expect(page).not.toHaveURL(/where=/);
  await page.getByRole("button", { name: "Filter", exact: true }).click();
  await page.getByRole("menuitem", { name: "Newer albums" }).click();
  await expect(page).toHaveURL(/where=/);
  await page.screenshot({ path: "/e2e-out/albums-saved-filter.png" });
});

/** The Albums count the tree shows, read from the API the tree reads: the page itself only refreshes it on navigation. */
async function albumCount(page: Page): Promise<number> {
  const response = await page.request.get("/api/v1/libraries");
  if (!response.ok()) return 0;
  const libraries = (await response.json()) as { id: number; levels: { key: string; count?: number }[] }[];
  return libraries.find((library) => library.id === 1)?.levels.find((level) => level.key === "albums")?.count ?? 0;
}

async function signIn(page: Page) {
  await page.goto("/");
  const username = page.getByLabel("Username");
  if (!(await username.isVisible().catch(() => false))) return; // already signed in
  await username.fill(USERNAME);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: /sign in|log in/i }).click();
  await expect(username).toBeHidden();
}
