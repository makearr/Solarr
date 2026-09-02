import createClient from "openapi-fetch";
import type { paths } from "./schema";

// Typed from schema.d.ts, generated from api/openapi.yaml (ADR-31).
// See documentation/api-generation.md.
export const api = createClient<paths>({
  baseUrl: "/",
  // The session is a cookie (ADR-14); it has to be sent.
  credentials: "same-origin",
});

// The stable part of the RFC 7807 contract: callers branch on this.
export type ProblemType =
  paths["/me"]["get"]["responses"][401]["content"]["application/problem+json"]["type"];
