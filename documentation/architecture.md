# Architecture: design decisions, and why Go

> **Level: 🔴 Expert.** How Solarr stays responsive with half a million files, and why it is built the way it is.

## Databases

Solarr keeps **four small SQLite databases instead of one large one**, all in **WAL mode**, each with **one writer**:

| Database | Holds |
|---|---|
| `library.db` | Everything that scales with your media |
| `settings.db` | Configuration and credentials; fully cached in memory |
| `jobs.db` | The work queue: high churn, kept apart |
| `statistics.db` | History and figures; the library stays correct with it empty |

Every write goes through that database's **internal writer queue** with priorities, so **your click jumps ahead of a
bulk scan** and the browser never stalls while Solarr works. No transaction runs over about 50 ms, and bulk work is split
into 200-row batches. There are no SQL joins across database files.

Measured on a synthetic **500,000-file library** (`make bench`; CI fails if a target is missed):

| Operation | Typical (p50) | Worst case (p99) | Target |
|---|---|---|---|
| A page of the library list, filtered | 1.7 ms | 3.5 ms | 100 ms |
| Search-as-you-type over titles | n/a | 17 ms | 150 ms |
| Import 200 rows | 3.7 ms | 5.7 ms | 50 ms |
| A user's edit while a bulk write runs | 4.3 ms | 19 ms | 25 ms |

A page 1,000 deep costs the same as page 1, and counts are kept as counters, never a live `COUNT(*)`. More in
[database layout](database-layout.md) and [benchmarks](benchmarks.md).

## Why Go

A media manager waits on disks, networks and databases, many things at once, for years without a restart. Go is built
for exactly that.

- **Parallel work is cheap and needs no framework.** A goroutine costs a few kilobytes and is scheduled by the Go
  runtime. Asking five metadata providers at once, probing files in parallel or serving hundreds of browser connections
  is ordinary code. No actor framework, no async runtime, no message broker.
- **Cancellation is part of the model.** A `context.Context` flows through requests, jobs and database writes, so
  abandoning a request or shutting down stops the work behind it.
- **Type-safe and compiled.** Errors are values the compiler makes you handle, domain values are types rather than loose
  strings. Strict linting (`golangci-lint`) and the race detector run in CI.
- **One static binary.** `CGO_ENABLED=0` builds a single executable with the web interface embedded, cross-compiled
  for amd64 and arm64. That is why the image is small and there is no runtime to install.
- **A small dependency surface.** The standard library's `net/http` with the `chi` router; a pure-Go SQLite driver.
- **Light at rest.** A fresh instance idles at about 45 MB of RAM.

## Three kinds of work, kept apart

| Kind | Example | Where it runs | Rule |
|---|---|---|---|
| **The browser** | Rendering a page, typing in search | In your browser; the server answers small, bounded requests | A request never does a long job inline |
| **User jobs** | Press *Import*, *Search*, *Drain this disk* | A job in the queue, started by your action, with progress you can watch | Priority over background work; shown in Activity; cancellable where it makes sense |
| **Background jobs** | Scans, RSS sync, the hunt, metadata refresh, artwork, backups | Registered scheduled tasks in their own worker pools | Never block the interface; every one visible and runnable on demand |

## Core concepts

- **A persistent job queue.** Work is a row in `jobs.db`, claimed with a lease. If the process dies mid-job, a reaper
  returns the job to the queue (and gives up on one that keeps killing its process). Jobs are for correctness;
  **events are lossy** and never a work queue.
- **Bounded worker pools.** Search, acquire, import, metadata, scan, rescan, files, moves and housekeeping each have
  their own pool, so a long scan cannot starve a search. **Import runs one at a time, by design.**
- **A priority queue in front of every database writer.** Writes carry a priority (interactive, normal, bulk) and
  waiting earns aging, so your click jumps ahead without letting background work starve.
- **Reads never wait for writes.** WAL mode lets the interface read while a scan writes, through a separate read pool.
- **A pure planner.** What to search for is decided by a function that only reads state and emits idempotent actions
  (each with a key): running it twice does nothing twice.
- **Live updates without polling storms.** Server-sent events coalesced on a 250 ms tick; each connection only receives
  what that user may see.
- **A thin, server-driven frontend.** Server data lives in TanStack Query; view state lives in the address bar.
- **Spec-first API.** One OpenAPI document generates the Go server interface and the TypeScript client, so frontend and
  backend cannot drift apart ([API generation](api-generation.md)).
- **Everything outbound is rate-limited and guarded**: per-provider pacing, circuit breakers, a destination guard.
- **Time and the filesystem are injected.** Code never calls the clock or the OS filesystem directly, which makes timing
  logic testable without sleeping; a linter enforces it.
- **Dependencies point one way.** An architecture test holds the allowed package imports as a list.
- **One generic core.** Media types are declarations at named extension points, not vertical modules.

More in the [codebase map](codebase-map.md), [web interface](web-interface.md) and [coding standards](coding-standards.md).
