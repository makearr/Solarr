# Changelog

Build numbers advance on each successful image build; see
`documentation/versioning.md`.

## 0.0.21 — 2026-09-04

- Add the naming template engine, live-verified against the real library (todo 4.5)


## 0.0.20 — 2026-09-04

- Generalize quality-profile targets beyond bitrate (ADR-40 amendment, todo 4.3)


## 0.0.19 — 2026-09-04

- Wire the measured half of ADR-39 attributes (todo 4.2)


## 0.0.18 — 2026-09-04

- Port Lidarr's release-name parser and quality vocabulary (ADR-18, todo 4.1)
- Bump build to 0.0.17 (internal)
- Add an artist column and configurable, sortable library columns
- Add person list/detail pages and redesign the work detail view
- Bump build to 0.0.16 (internal)
- Add the person read/subscribe API (ADR-32 phase 5)
- Expose credits, genres, description and on-disk status on GET /works/{id}
- Add work.description, sourced from the release group's own Wikidata QID
- Add internal/wikidata and wire real biographies into EnrichJob
- Wire artist credits and genre tags into EnrichJob
- Add artist/credit/genre data and a library.People resolver (ADR-32/33)
- Serve and render cover art (section 21.6, closing the loop)
- Bump build to 0.0.15 (internal)
- Fix internal/parse and SearchIdentifier bugs found by live verification
- Bump build to 0.0.14 (internal)
- Add MusicBrainz text search and wire ADR-24's parse strategy (section 10.2)
- Bump build to 0.0.13 (internal)
- Wire the artwork pipeline into a job handler (section 21.6)
- Bump build to 0.0.12 (internal)
- Add POST /api/v1/score/simulate (F6, section 13.2)
- Validate a quality profile's real shape on write, not just its JSON syntax
- Bump build to 0.0.11 (internal)
- Bump build to 0.0.10 (internal)
- Implement internal/score: ADR-40 quality/language interpreter
- Add internal/artwork resize-and-encode pipeline and Cover Art Archive client
- Bump build to 0.0.9 (internal)
- Add internal/match: section 10.2's weighted-distance identity matcher
- Bump build to 0.0.8 (internal)
- Round-trip quality and language profiles through config export/import
- Declare internal/artwork in the import allowlist
- Declare internal/score and internal/match in the import allowlist
- Bump build to 0.0.7 (internal)
- Add make binfmt: register qemu emulators before a multi-arch image build
- Add quality and language profile schema (ADR-40, section 19.8)
- Bump build to 0.0.6 (internal)
- Add internal/parse: stage 4's ADR-18 music release-name parser + golden corpus
- Bump build to 0.0.5 (internal)
- Collapse the sidebar to icons below 1100px (section 21.8)
- Bump build to 0.0.4 (internal)
- Trim comments to CLAUDE.md's one-line rule before merge
- Add GET /api/v1/works/{id}: read-only work detail page
- Bump build to 0.0.3 (internal)
- Log a startup warning when ADR-30's external tools are missing
- Bump build to 0.0.2 (internal)
- Add GET /api/v1/library/unidentified: F29's manual identification queue
- Add category counts: the sidebar's other, deliberately-deferred half
- Add GET /api/v1/search: ranked, category-filtered title search
- Make the MusicBrainz metadata cache staleness-aware (ADR-29)
- Mark vanished library paths MISSING after a full sweep (section 10.4)
- Add GET /api/v1/categories and wire it into the sidebar and library filter
- Bundle fpcalc and ffprobe in the runtime image (ADR-30)
- Replace the Library placeholder with the real browse page
- Add GET /api/v1/works: the library list endpoint
- Add work_root_folder: a temporary bridge for category filtering
- Wire ADR-29's provider into real edition/work_part enrichment
- Add internal/musicbrainz: ADR-29's provider, both sources
- Add cache/metadata.db: a generic, disposable metadata cache
- Fix env overrides misreading YAML-special leading characters
- Populate a placeholder edition from the release MBID
- Add ADR-15's backup rotation, wired in for real
- Benchmark the scan batch write path at ADR-22 scale
- Add the scan.Consumer: files on disk become identified works
- Persist scan progress and add the first job handler (section 10.4)
- Add internal/scan: the section 10.4 mtime sweep, resumable
- Add the embedded-MBID identifier: identity with no network call (ADR-29)
- Add internal/mediatool: ADR-30's tools, and settle what stage 3 needs
- Recover from a corrupt database at startup, and reconcile after (ADR-15)
- Add backup retention (ADR-15)
- Add discovery_job (ADR-38) and wire it into Invariant 41
- Add the request table's query surface (ADR-14, stage 12)
- Add counter reconciliation logic
- Add internal/metaresolve: field-level provider priority (ADR-8, Inv. 13)
- Add internal/model: ADR-23 extension types and the ADR-24 Identifier chain
- Update benchmarks.md now that FTS5 search exists
- Add FTS5 search over titles and aliases (migration 0010)
- Add event_log and retention_state to jobs.db, decision_rollup to library.db
- Verify the image can supply ADR-30's tools, and qualify the 0.2 milestone
- Give the list's view state to the router, and a DOM to test it with
- Compose the list query instead of writing its filters as no-ops
- Size the page cache per database, and stop pretending the driver is a setting
- Checkpoint the log on purpose instead of by accident
- Measure the ADR-28 targets against a 500k corpus, and settle ADR-5
- Add user management to the interface
- Require an account at setup; open access stays a settings decision
- Unstick the create-account button, and make the login requirement a policy
- Loosen passwords, add themes and icons, and report the egress address
- Fix the API base URL, and build the shell around a dashboard
- Drop history.db: three databases, not four
- Stop interning what is unique per file
- Give parts an identity, and fix the three defects the validation found
- Cut SQL comments to one line, and move the prose to documentation
- Complete library.db: collections, ready queue, artwork, statistics, reputation
- Add people, organisations, tags and subscriptions
- Add candidates, decisions, provenance and quarantine
- Add desired state, the work-to-file link, the hunt queue and counters
- Plan eleven media types, and add classification and image attributes
- Add the physical layer: objects, paths and interned attributes
- Close five open stage 1 points
- Create library.db and the identity schema
- Tidy doubled references left by the comment pass
- Cut every remaining comment block to one line
- Cut comments back to one line, and add TZ
- Make PUID and PGID authoritative, and say so when they cannot be
- Add F9 config export/import, and stop enforcing host permissions
- Add useLibraryList(), the headless core both library views render
- Add the first-run wizard: claim the install, add a library folder
- Run as `media` 1000:1000, overridable with PUID and PGID
- Make the image work on a first `docker run`, and a fresh config directory
- Add the web interface shell, embedded in the binary
- Implement the generated ServerInterface, and test routing against the spec
- Apply environment overrides on the first run, not just later ones
- Give 405 its own problem type instead of reusing bad-request
- Generate the Go types and TS client from api/openapi.yaml
- Correct the versioning rules and tighten the build context
- Create credential files 0600 instead of world-readable
- Refuse to commit local-only working documents
- Record that database placement is the operator's responsibility
- Add a codebase map to the wiki
- Add the event bus and the SSE stream
- Add cursor pagination and the jobs listing endpoint
- Make multi-arch image builds work
- Add the Dockerfile, healthcheck subcommand and compose example
- Add authentication: passwords, API keys and trusted proxy headers
- Add the HTTP server and a working serve command
- Add job workers, the executor and the dispatch loop
- Add the durable job queue
- Add the authz access context
- Add settings.db, the sqlc stack and the in-memory config cache
- Add secrets: encryption at rest and a type that cannot leak
- Add first-run config.yml creation and key generation
- Add startup directory checks and `config check`
- Add environment overrides for config.yml
- Add config.yml loading and validation
- Add migrations and the pre-migration backup
- Add the priority write queue
- Add the store connection layer
- Add the httpx circuit breaker and Client
- Add the httpx rate limiter
- Add the httpx SSRF guard
- Use '_' when a name contains a path separator
- Add the fsx filesystem seam and path-safety choke point
- Add project skeleton and the clock seam
- Publish to GitHub on every build
- Version by milestone and build, not by commit
- Record deployment and toolchain decisions
- Add Makefile wrapping Go and Node toolchains in Docker
- Support Servarr metadata proxies as preferred, never required
- Adopt generated-code stack: sqlc, OpenAPI spec-first, chi, slog
- Adopt AGPL-3.0-or-later
- Add documentation wiki with coding standards
- Add versioned git hooks and CI workflow
- Rewrite README around functionality
- Ignore local-only CLAUDE.md working context


## Unreleased

Design phase. No image has been built yet, so no build has been recorded.
The first entry will appear when `make image` first succeeds.
