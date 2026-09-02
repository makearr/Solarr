# API generation

`api/openapi.yaml` generates the Go server interface and the TypeScript client (ADR-31).
Generated output is committed and CI fails on a stale diff.

## The spec describes what exists

Architecture section 13.2 lists the endpoints the finished product will carry. Specifying them
before they are built would generate a client full of calls that 404, and since the output is
committed the drift would be permanent. Each stage adds its own.

## OpenAPI 3.0.3, not 3.1

oapi-codegen does not support 3.1 and does not fail on it — it drops what it cannot read.
Written in 3.1 first, three `const` response fields silently generated as plain `string`; on
3.0.3 they are typed constants.

Revisit when oapi-codegen issue 373 closes.

## Response enums live under `components`

oapi-codegen emits an enum type only for a named schema. Inline in a response body it generates
a reference to a type it never defines, and the output does not compile.

## `ServerInterface`, not `StrictServerInterface`

The strict variant's handlers return a response *object* that its wrapper then writes, which
cannot express a long-lived SSE stream — and that wrapper is the same ResponseWriter-wrapping
hazard that has silently broken streaming here before.

`ServerInterface` hands the handler the raw writer and still provides typed parameters, so the
compile-time guarantee is kept without putting the event stream at risk.

## What the two directions catch

`var _ gen.ServerInterface = (*Server)(nil)` makes an operation added to the spec stop the build
until a handler exists.

`TestRoutesMatchTheSpec` walks chi's actual routing table and compares both ways. A route the
spec does not describe is missing from the generated TypeScript client, so the UI cannot call it
and nobody finds out until a feature does not work.

## Routing stays hand-written

The generated router registers every route flat, which would dissolve the authenticated group.
Its value is that adding a route outside it is a visible act.

## `embedded-spec` is off

It pulls in kin-openapi purely so a test can read the document. The drift test parses the YAML
with the library already in `go.mod`.
