import createClient from "openapi-fetch";
import type { paths } from "./schema";

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

// The stable part of the RFC 7807 contract: callers branch on this.
export type ProblemType =
  paths["/me"]["get"]["responses"][401]["content"]["application/problem+json"]["type"];
