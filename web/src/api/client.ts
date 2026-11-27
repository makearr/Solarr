import { t } from "@/lib/i18n";
import { showToast } from "@/lib/toast";
import createClient from "openapi-fetch";
import type { paths } from "./schema";
import { queryClient } from "./queryClient";
import { fromBase64, sealSecrets, sealWith } from "./seal";

/** Every versioned endpoint lives under this prefix, and the paths in
 *  schema.d.ts are relative to it. Kept in step with `servers` in
 *  api/openapi.yaml by client.test.ts -- getting this wrong does not fail
 *  loudly, it falls through to the SPA handler and returns index.html with a
 *  200, so every query silently yields nothing. */
export const API_PREFIX = "/api/v1";

// Typed from schema.d.ts, generated from api/openapi.yaml (ADR-31).
// See documentation/api-generation.md.
export const api = createClient<paths>({
  baseUrl: API_PREFIX,
  // The session is a cookie (ADR-14); it has to be sent.
  credentials: "same-origin",
});

/** ok answers a call's data or throws its problem: what every query and mutation wants of a response. */
export async function ok<T>(call: Promise<{ data?: T; error?: unknown; response?: Response }>): Promise<T> {
  const { data, error, response } = await call;
  // The status decides, not the body: a proxy's empty 502 parses to an error of "", which is falsy.
  if (error !== undefined || (response !== undefined && !response.ok)) {
    throw error || { status: response?.status, title: response?.statusText || "The request failed" };
  }
  return data as T;
}

// The stable part of the RFC 7807 contract: callers branch on this.
export type ProblemType =
  paths["/me"]["get"]["responses"][401]["content"]["application/problem+json"]["type"];

let transportKey: Promise<Uint8Array | null> | undefined;

/** serverTransportKey fetches the key secrets are sealed with, once; without one they go as they are. */
function serverTransportKey(): Promise<Uint8Array | null> {
  transportKey ??= fetch(`${API_PREFIX}/auth/transport-key`, { credentials: "same-origin" })
    .then(async (response) => (response.ok ? fromBase64(((await response.json()) as { public_key: string }).public_key) : null))
    .catch(() => null)
    .then((key) => {
      // A failure is not kept: one blip (a restart) would otherwise send every later secret of this tab unsealed.
      if (key === null) transportKey = undefined;
      return key;
    });
  return transportKey;
}

const SECRET_FIELD = /"(password|api_key|credentials|[a-z]+_api_key)"\s*:\s*"(?:[^"\\]|\\.)*"/;

// A failure with no body (a proxy's 502 while the server restarts) parses to an error of "", which every `if (error)`
// reads as success: it is given a problem body here, once, for every call.
api.use({
  async onResponse({ response }) {
    if (response.ok || (await response.clone().text()) !== "") return undefined;
    return new Response(JSON.stringify({ status: response.status, title: response.statusText || "The request failed" }), {
      status: response.status, statusText: response.statusText, headers: { "Content-Type": "application/problem+json" },
    });
  },
});

// A settings field that saves on change confirms itself once, wherever it lives; the server's refusal arrives as an error toast.
const AUTO_SAVED = ["/settings/", "/categories/", "/metadata/"].map((path) => API_PREFIX + path);
api.use({
  onResponse({ request, response }) {
    if (!response.ok || !["PUT", "PATCH"].includes(request.method)) return undefined;
    if (AUTO_SAVED.some((prefix) => new URL(response.url, "http://solarr.test").pathname.startsWith(prefix))) {
      showToast({ message: t("toast.saved") });
    }
    return undefined;
  },
});

// Answers that are expected to be 401 without the session having ended.
const SESSION_PROBES = ["/auth/login", "/me", "/auth/transport-key"].map((path) => API_PREFIX + path);
let sessionEndedAt = -Infinity;

// An expired or revoked session sends the interface back to sign-in: App shows Login once /me answers 401.
api.use({
  onResponse({ response }) {
    if (response.status !== 401 || SESSION_PROBES.includes(new URL(response.url, "http://solarr.test").pathname)) return undefined;
    // A screen's worth of parallel 401s asks /me once.
    if (Date.now() - sessionEndedAt < 2_000) return undefined;
    sessionEndedAt = Date.now();
    void queryClient.invalidateQueries({ queryKey: ["me"], exact: true });
    return undefined;
  },
});

// Passwords and API keys are sealed for the server before they leave the browser (internal/api/auth/transport.go).
api.use({
  async onRequest({ request }) {
    if (!["POST", "PUT", "PATCH"].includes(request.method) || !request.headers.get("Content-Type")?.includes("application/json")) {
      return undefined;
    }
    const text = await request.clone().text();
    if (!SECRET_FIELD.test(text)) return undefined;
    const key = await serverTransportKey();
    if (key === null) return undefined;
    const sealed = sealSecrets(JSON.parse(text), (plain) => sealWith(key, plain));
    return new Request(request, { body: JSON.stringify(sealed) });
  },
});
