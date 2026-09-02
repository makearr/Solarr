# Codebase map

Where things live and what depends on what. The dependency rules here are enforced by
`internal/archtest`, which holds the graph as an explicit allowlist: a new package fails the
build until it declares what it imports.

## Packages

Roughly in dependency order — everything below depends only on things above it.

| Package | Responsibility |
|---|---|
| `internal/clock` | Injected time. No production code calls `time.Now` (ADR-17) |
| `internal/privilege` | `PUID`/`PGID`/`TZ`, ownership of managed directories, the privilege drop |
| `internal/fsx` | Injected filesystem, and the one place paths are made safe (ADR-14) |
| `internal/secrets` | Encryption at rest, and a `Secret` type that redacts itself everywhere |
| `internal/paginate` | Cursor pagination. `OFFSET` is banned |
| `internal/httpx` | Every outbound request: SSRF guard, rate limiter, circuit breaker |
| `internal/events` | Bounded, lossy notification bus |
| `internal/config` | `config.yml` bootstrap: parse, validate, env overrides, first run |
| `internal/store` | One writer and a read pool per database; migrations; backups |
| `internal/settings` | `settings.db`, cached as an immutable snapshot |
| `internal/authz` | Per-category access. The category repository |
| `internal/auth` | Passwords, API keys, sessions |
| `internal/jobs` | Durable queue, executor, dispatcher |
| `internal/health` | Liveness and readiness |
| `internal/apiserver` | Routing, problem responses, authentication, SSE, setup, settings |
| `internal/webui` | The compiled interface, embedded and served from the binary |
| `cmd/medianexus` | Wiring only. No logic |

`migrations/` holds embedded SQL, one directory per database; `queries/` holds sqlc source;
`internal/store/gen/` and `internal/apiserver/gen/` hold generated code that is committed and
never hand-edited. `web/` is the interface source; `make web` compiles it into
`internal/webui/dist`, which is generated and gitignored.

Which database a table belongs in, and why the boundary is where it is:
`database-layout.md`.

## The front end

`web/src/list/` is the headless list core. `useLibraryList()` owns fetching, cursor pagination,
sort and filter state, the selection model and virtualisation; `ListTable` and `ListGrid`
decide only what a row looks like. A new filter, column or bulk action is implemented once, in
the hook. See `web-interface.md`.

`api/openapi.yaml` is the source of truth for the HTTP surface. It generates the Go server
interface and the TypeScript client, and `*Server` is asserted to implement the interface, so
an operation added to the spec stops the build until a handler exists. See
`api-generation.md`.

## The seams

Three things exist so the rest can be tested without a clock, a disk or a network, and are
enforced by `forbidigo` rather than by convention:

- `clock.Clock` — no `time.Now`, no `time.NewTicker`
- `fsx.FS` — no `os` filesystem calls
- `httpx.Client` — no bare `http.Get`

The only exemptions are the seam implementations themselves, and `cmd/`, which writes to
standard output by definition.

## Where authorisation happens

In the repository layer, never in a handler (Invariant 36):

- `authz.Access.Categories()` is the category repository. Handlers call it rather than reaching
  for the settings snapshot.
- `jobs.Queue.ListPage` takes a required `Visibility` and narrows **in the query**. Filtering
  results afterwards returns short pages and breaks the cursor.
- The SSE stream filters each event before buffering it, not at render time.

A category the caller cannot see is 404, never 403. A category they can see but may not act on
is 403. Collapsing the two either leaks private categories or disguises ordinary permission
errors as missing records.

## Where the dry-run gate is

One place: `jobs.Executor`. It refuses to run a job type declared mutating when the mode is
`observe` or `shadow`, and nothing else in the codebase consults the mode (ADR-20,
Invariant 31). A flag checked in many places is a flag that gets missed in one.

## Reading order for a newcomer

1. `internal/clock` and `internal/fsx` — small, and they establish the testing style.
2. `internal/store` — the write queue explains the concurrency model.
3. `internal/jobs` — the queue, then the executor, then the dispatcher.
4. `internal/apiserver/server.go` — how a request becomes an identity and then an answer.
5. `cmd/medianexus/serve.go` — the startup order, which is architecture section 15.1 in code.
