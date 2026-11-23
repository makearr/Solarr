import { createServer, type IncomingMessage } from "node:http";
import { createHmac } from "node:crypto";
import { test, expect, type Page } from "@playwright/test";

const USERNAME = "admin";
const PASSWORD = "TestPass123!TestPass";
/** The address the instance under test reaches this process at: the docker bridge gateway by default, 127.0.0.1 for a local binary. */
const RECEIVER_HOST = process.env.E2E_RECEIVER_HOST ?? "172.17.0.1";
const RECEIVER_PORT = Number(process.env.E2E_RECEIVER_PORT ?? 15499);
const SECRET = "e2e-signing-secret";

type Received = { signature: string | undefined; body: string };

test.describe.configure({ mode: "serial" });

async function signIn(page: Page) {
  await page.goto("/");
  const username = page.getByLabel("Username");
  if (!(await username.isVisible().catch(() => false))) return;
  await username.fill(USERNAME);
  await page.getByLabel("Password", { exact: true }).fill(PASSWORD);
  await page.getByRole("button", { name: /sign in|log in/i }).click();
  await expect(username).toBeHidden();
}

test("an outgoing webhook can be added, shows the selected preset's body, and a test reaches a signed receiver", async ({ page }) => {
  const received: Received[] = [];
  const receiver = createServer((request: IncomingMessage, reply) => {
    const chunks: Buffer[] = [];
    request.on("data", (chunk: Buffer) => chunks.push(chunk));
    request.on("end", () => {
      received.push({ signature: request.headers["x-solarr-signature"] as string | undefined, body: Buffer.concat(chunks).toString() });
      reply.writeHead(200, { "content-type": "text/plain" });
      reply.end("received");
    });
  });
  await new Promise<void>((resolve) => receiver.listen(RECEIVER_PORT, "0.0.0.0", resolve));
  try {
    await signIn(page);
    await page.goto("/connections/webhooks");
    await expect(page.getByRole("heading", { name: "Webhooks" })).toBeVisible();
    await page.getByRole("button", { name: "Add outgoing webhook" }).click();

    const form = page.getByRole("region", { name: "Outgoing webhook" });
    await form.getByLabel("Name").fill("E2E receiver");
    await form.getByLabel("Format").selectOption("discord");
    await form.getByLabel("Address").fill(`http://${RECEIVER_HOST}:${RECEIVER_PORT}/hook`);
    await form.getByLabel("Signing secret (optional)").fill(SECRET);

    // The panel shows the body of the chosen preset, not a fixed sample.
    await form.getByText("What will be sent").click();
    await expect(form.locator("pre")).toContainText('"content"');
    await form.getByLabel("Format").selectOption("generic");
    await expect(form.locator("pre")).toContainText('"event"');

    await form.getByRole("checkbox", { name: "New files were imported" }).check();
    await form.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("E2E receiver")).toBeVisible();

    await page.getByRole("button", { name: "Edit" }).first().click();
    await page.getByRole("button", { name: "Send test" }).click();
    await expect(page.getByText("The receiver answered 200.")).toBeVisible();

    expect(received).toHaveLength(1);
    expect(received[0].signature).toContain(createHmac("sha256", SECRET).update(received[0].body).digest("hex"));
    expect(JSON.parse(received[0].body).event).toBe("test");
  } finally {
    receiver.close();
  }
});

test("an incoming webhook shows its token once", async ({ page }) => {
  await signIn(page);
  await page.goto("/connections/webhooks");
  await page.getByRole("button", { name: /Add incoming webhook/ }).click();
  await page.getByRole("button", { name: "Save" }).last().click();
  await expect(page.getByText(/curl/).first()).toBeVisible();
});
