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
│               skyhook  tvmaze  tmdb  tvdb  openlibrary  googlebooks  audible
├── identify/   model  parse  parsetv  parsebook  match  score  priority
├── scanning/   scan  probe
├── importing/  inspect  importjob  batch  placer  rules  arrimport
├── moving/     mover  moverjob  drainjob  renamejob  placement  naming  sidecar
├── acquiring/  indexer (Newznab/Torznab client, indexer base and catalog)  download (client interface and bases)
│               poll  plan  judge  dispatch  rss
│               indexers: indexer/thepiratebay  nyaa  yts  knaben  internetarchive  subsplease  gazelle  filelist
│               hdbits  passthepopcorn  beyondhd  broadcasthenet  soulseek
│               clients: sabnzbd  nzbget  qbittorrent  transmission (+ Vuze)  deluge  rtorrent  utorrent  flood  aria2
│               slskd  blackhole (Torrent and Usenet)
├── media/      the media-type registry  +  music  tv  movie  book (books and audiobooks)
│               +  shared by types: works  knownfor  picturesweep  housekeeping
└── archtest/   layering, access and Invariant-90 checks, as tests
```

| Group | What it holds |
|---|---|
| `api` | HTTP surface: routing, problem responses, SSE, authentication, per-category access, probes, the embedded UI |
| `platform` | Seams and infrastructure with no domain knowledge: time, filesystem, outbound HTTP, config, secrets, external tools, archives, image encoding |
| `db` | One writer and a read pool per database (`store`), `settings.db` cached in memory, the `library.db` repositories, post-restore reconciliation |
| `jobs` | The durable queue, and the maintenance jobs that belong to no pipeline |
| `metadata` | Providers and the shared cache in front of them. `metaresolve` holds how sources are asked and merged: `FallThrough` (in order, first answer: music's proxies then MusicBrainz), `AskAll` (at once, each in its own rate bucket: TV's TMDB, AniList, TVmaze) and `Resolve`, the one rule for a stored field (yours, else the newest) |
| `identify` | Pure decision logic, no I/O: domain types, the release-name parsers (music; `parsetv` for TV, Sonarr's patterns on regexp2), identity matching, quality scoring, acquisition priority |
| `scanning` | The library sweep and the background probe that fills measured attributes |
| `importing` | Import inspection (WALK to DECIDE) and its resumable job; `placer` is the placing every media type shares (root, transfer, blocked content, permissions, extras, undo), `rules` Radarr's and Sonarr's import rules (`documentation/import-rules.md`) |
| `moving` | The move engine, its apply job, drain orchestration, root-folder placement, path templates; `sidecar` writes the nfo and picture files media servers read |
| `acquiring` | Stage 5's protocol clients: Newznab/Torznab search and release fetch (ADR-37/43/47); `download.Client`, the download-client interface, with SABnzbd behind it (NZB uploaded by `addfile`, as Lidarr does); `poll`, the per-minute follow of every sent download to imported and cleaned (§10.9); `plan`, ADR-6's planner (desired items to `hunt_queue`) and the hunt that searches within the hourly budget. `judge` is what every media type's search shares (asking the indexers, ranking, de-duplicating, the delay profile, recording a decision), `dispatch` sends the best decided release, and `rss` is the one RSS sync: it reads each indexer's feed and each media type with the `Feeding` capability judges it against its own wanted items. What a release is and which wanted item it covers is the media type's |
| `media` | `internal/media` is the registry: each media type is a `Module` with a `Descriptor` (key, label, vocabulary, levels, roles) and optional capabilities such as `Naming`. Core asks the registry instead of naming a type; `cmd/solarr/app/registered.go` is the one file that lists the modules. `GET /mediatypes` serves the descriptors, and a library can only be created for a registered type. The library sweep is one job (`library.scan`): it asks the registry for the `Scanning` capability of the media type a library holds and hands that type's consumer the files; a type's own job types come through the `Jobs` capability. `registered.go` builds each module with what it needs from the process. Files that still import a module directly are listed in `internal/archtest/media_test.go`; that list only shrinks. `cmd/solarr/app/third_type_test.go` is the seam's proof: a fake movie type, registered through its module alone, listed, added, credited, followed, searched and placed by the shared endpoints. A type's lead credit is `People.AddLeadCredit` in its own lead role, and a work without parts says what it needs through `AlbumRequest.RequiredWithoutParts`. `internal/media/works` is the one implementation TV, movies, books and audiobooks share of what they do with their works: `Search[W]` (the candidate pipeline, the library's rules, the upgrade rule), `Grab`, `Searching`, `Feed`, `Cutoff`, `Catalog`, `Files` and `RefreshJob`. Each type keeps only its specialisation: a `Spec` (what an item wants, its queries, what a release is), its files for the cutoff, a `Source` for adding, a feed `Matcher`. Music is not on it: its albums are identified and grabbed track by track |

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

## Job pools

A job type runs in one pool, and each pool has its own workers, so one kind of work waits only for its own kind
(`internal/jobs/job.go`, sizes in `cmd/solarr/app/serve.go`):

| Pool | Workers | Runs |
|---|---|---|
| `search` | `workers.search` (4) | planner, hunt |
| `acquire` | `workers.acquire` (2) | download poll, download-folder watch |
| `import` | 1, fixed (ADR-3) | imports |
| `metadata` | `workers.metadata` (4) | enrichment, refresh, artwork, follows |
| `scan` | 2 | library sweeps, one job per root |
| `files` | 2 | attribute probe, cutoff checks, tag write-back |
| `move` | 1 | moves, renames, linking |
| `housekeeping` | 2 | counters, statistics, capacity, cache, recycle bin and retention sweeps |
| `maintenance` | 1 | database maintenance, backups |

Every job still writes through its database's single writer, so more workers add disk and provider load, never
concurrent writes. A job type whose pool has no workers would be queued and never run; a test fails on that.

## The golden answers

`cmd/solarr/app/golden_test.go` builds the real server over real databases in a temporary directory
(`process.newServer`, nothing listening), seeds one invented album and one invented series through the
same write paths the application uses, and compares what the list and detail endpoints answer with
`cmd/solarr/app/testdata/golden/*.json`. A change to an answer fails the test; when the change is
meant, re-record with `go test ./cmd/solarr/app -run Golden -update` and review the diff. Values that
are the current time are replaced before comparing.

## Reading order for a newcomer

1. `internal/platform/clock` and `internal/platform/fsx` — small, and they set the testing style.
2. `internal/db/store` — the write queue explains the concurrency model.
3. `internal/jobs` — the queue, then the executor, then the dispatcher.
4. `internal/api/apiserver/server.go` — how a request becomes an identity and then an answer.
5. `cmd/solarr/app/serve.go` — the startup order, architecture section 15.1 in code.
