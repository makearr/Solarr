import { defineConfig } from "@playwright/test";

/** One browser, one worker, no retries: this suite answers "does the thing
 *  work", and a flaky pass is worse than a failure. The harness (make e2e)
 *  owns the instance, so there is no webServer here. */
export default defineConfig({
  testDir: ".",
  testMatch: /.*\.spec\.ts/,
  workers: 1,
  retries: 0,
  timeout: 120_000,
  expect: { timeout: 20_000 },
  reporter: [["list"], ["html", { open: "never", outputFolder: "/e2e-out/report" }]],
  outputDir: "/e2e-out/artifacts",
  use: {
    baseURL: process.env.SOLARR_URL ?? "http://127.0.0.1:15455",
    screenshot: "only-on-failure",
    video: "off",
    trace: "retain-on-failure",
  },
});
