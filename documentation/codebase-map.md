# Codebase map

Where things live and what depends on what. The dependency rules are enforced by
`internal/archtest`, which holds the graph as an explicit allowlist: a new package fails the
build until it declares what it imports.

## Layout

Packages are grouped by pipeline. Each leaf directory is one Go package and its name is the
package name, so `internal/metadata/musicbrainz` is imported as `musicbrainz`.

```text
cmd/solarr/            CLI: serve, config check, user, healthcheck, version
├── app/                   the server process: startup order, job registration (job_*.go),
│                          schedules, shutdown
└── adapters/              bridges apiserver/job interfaces to library and settings

internal/
├── api/        apiserver (+gen)  auth  authz  health  webui
├── platform/   clock config egress events fsx httpx paginate privilege secrets
│               mediatool archive artwork
├── db/         store (+gen)  settings  library (+fixture)  recovery
├── jobs/       the durable queue, executor, dispatcher, task registry
│               + capacity  cachebudget
├── metadata/   musicbrainz  wikidata  acoustid  metacache  metaresolve
│               skyhook  tvmaze  tmdb  tvdb
├── identify/   model  parse  parsetv  match  score  priority
├── scanning/   scan  probe
├── importing/  inspect  importjob
├── moving/     mover  moverjob  drainjob  placement  naming
├── acquiring/  indexer (Newznab/Torznab client)  download (client interface)  sabnzbd  poll  plan
├── media/      the media-type registry  +  music  tv
└── archtest/   layering, access and Invariant-90 checks, as tests
```

| Group | What it holds |
|---|---|
| `api` | HTTP surface: routing, problem responses, SSE, authentication, per-category access, probes, the embedded UI |
| `platform` | Seams and infrastructure with no domain knowledge: time, filesystem, outbound HTTP, config, secrets, external tools, archives, image encoding |
| `db` | One writer and a read pool per database (`store`), `settings.db` cached in memory, the `library.db` repositories, post-restore reconciliation |
| `jobs` | The durable queue, and the maintenance jobs that belong to no pipeline |
| `metadata` | Providers and the shared cache in front of them; field-level provider merge |
| `identify` | Pure decision logic, no I/O: domain types, the release-name parsers (music; `parsetv` for TV, Sonarr's patterns on regexp2), identity matching, quality scoring, acquisition priority |
| `scanning` | The library sweep and the background probe that fills measured attributes |
| `importing` | Import inspection (WALK to DECIDE) and its resumable job |
| `moving` | The move engine, its apply job, drain orchestration, root-folder placement, path templates |
| `acquiring` | Stage 5's protocol clients: Newznab/Torznab search and release fetch (ADR-37/43/47); `download.Client`, the download-client interface, with SABnzbd behind it (NZB uploaded by `addfile`, as Lidarr does); `poll`, the per-minute follow of every sent download to imported and cleaned (§10.9); `plan`, ADR-6's planner (desired items to `hunt_queue`) and the hunt that searches within the hourly budget. What importing and searching mean is supplied by the media type; `internal/media/music` owns the candidate pipeline |
| `media` | `internal/media` is the registry: each media type is a `Module` with a `Descriptor` (key, label, vocabulary, levels, roles) and optional capabilities such as `Naming`. Core asks the registry instead of naming a type; `cmd/solarr/app/registered.go` is the one file that lists the modules. `GET /mediatypes` serves the descriptors, and a library can only be created for a registered type. Files that still import a module directly are listed in `internal/archtest/media_test.go`; that list only shrinks |

`migrations/` holds embedded SQL, one directory per database; `queries/` holds sqlc source.
`internal/db/store/gen/` and `internal/api/apiserver/gen/` are generated, committed and never
hand-edited. `web/` is the interface source; `make web` compiles it into
`internal/api/webui/dist`, which is generated and gitignored.

Which database a table belongs in, and why: `database-layout.md`.

## The front end

`web/src/list/` is the headless list core. `useLibraryList()` owns fetching, cursor pagination,
sort and filter state, the selection model and virtualisation; `ListTable` and `ListGrid`
decide only what a row looks like. See `web-interface.md`.

`api/openapi.yaml` is the source of truth for the HTTP surface. It generates the Go server
interface and the TypeScript client, and `*Server` is asserted to implement the interface, so
an operation added to the spec stops the build until a handler exists. See `api-generation.md`.

## The seams

Enforced by `forbidigo`, not by convention:

- `clock.Clock` — no `time.Now`, no `time.NewTicker`
- `fsx.FS` — no `os` filesystem calls
- `httpx.Client` — no bare `http.Get`

The only exemptions are the seam implementations themselves and `cmd/`, which writes to
standard output by definition.

## Where authorisation happens

In the repository layer, never in a handler (Invariant 36):

- `authz.Access.Categories()` is the category repository. Handlers call it rather than reading
  the settings snapshot; `archtest` fails a handler that does.
- `jobs.Queue.ListPage` takes a required `Visibility` and narrows **in the query**. Filtering
  afterwards returns short pages and breaks the cursor.
- The SSE stream filters each event before buffering it, not at render time.

A category the caller cannot see is 404, never 403. One they can see but may not act on is 403.

## Where the dry-run gate is

One place: `jobs.Executor`. It refuses to run a job type declared mutating when the mode is
`observe` or `shadow`; nothing else consults the mode (ADR-20, Invariant 31).

## Reading order for a newcomer

1. `internal/platform/clock` and `internal/platform/fsx` — small, and they set the testing style.
2. `internal/db/store` — the write queue explains the concurrency model.
3. `internal/jobs` — the queue, then the executor, then the dispatcher.
4. `internal/api/apiserver/server.go` — how a request becomes an identity and then an answer.
5. `cmd/solarr/app/serve.go` — the startup order, architecture section 15.1 in code.
