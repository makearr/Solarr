import createClient from "openapi-fetch";
import type { paths } from "./schema";

// The client is typed from schema.d.ts, which is generated from
// api/openapi.yaml (ADR-31). Calling an endpoint that does not exist, or reading
// a field the server does not send, is a TypeScript error rather than a runtime
// surprise -- which is the entire reason the spec was written before this app.
//
// Relative baseUrl: production serves the UI and the API from one origin, and
// dev routes /api through the Vite proxy, so there is no environment-specific
// base URL to get wrong.
export const api = createClient<paths>({
  baseUrl: "/",
  // The session is a cookie (ADR-14); it has to be sent.
  credentials: "same-origin",
});

// The API answers RFC 7807 for every error, including 404 and 405. The `type`
// slug is the stable part of that contract, so callers branch on it rather than
// on a status code or a message.
export type ProblemType =
  paths["/me"]["get"]["responses"][401]["content"]["application/problem+json"]["type"];
