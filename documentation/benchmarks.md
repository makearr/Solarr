# Benchmarks

ADR-28 states performance as numbers, and numbers only mean something against a corpus.
`internal/library/fixture` builds one; `internal/library/bench_test.go` measures against it.

## The corpus

`fixture.Ensure` generates a `library.db` and caches it under
`internal/library/testdata/fixtures/`, named for its spec. Generation is deterministic —
same spec, same rows — so two runs, or two drivers, compare like with like. Nothing in
that directory is committed (`*.db` is gitignored); delete it to force a rebuild.

The mix follows the real library: roughly 50% music, 35% TV, 15% film, with music and TV
works carrying parts and a film satisfied by one file. Three details are deliberate:

- **Sort titles repeat.** Every eighth work shares one title, so a page boundary lands
  inside a group of equal sort keys. A cursor without its id tie-break drops rows there,
  and that has already happened once (notes section 11).
- **A tenth of the files are unfingerprinted**, so ADR-35's resumable cursor has work to
  find at any scale.
- **Timestamps come from a fixed epoch**, never the clock.

Scale is `MEDIANEXUS_BENCH_FILES`, defaulting to 20,000 files so an ordinary `make test`
stays quick. ADR-22's figure is 500,000:

```sh
make bench BENCH_FILES=500000
```

## The targets

`TestADR28_*` measure p99 over repeated runs and fail when a target is missed. They are
ordinary tests, not benchmarks, because a benchmark reports and a test enforces — ADR-28
asks for enforcement.

A target is a ceiling stated at 500k files, so a smaller corpus has headroom and a breach
at 20k is a plan or index regression rather than noise.

Full-text search has no test yet: FTS5 is not built. The indexed title lookup stands in
its place and is measured against the same target.

## The driver (ADR-5)

The driver is named in one place, behind a build tag: `internal/store/driver_modernc.go`
and `driver_ncruces.go`. `make bench-drivers` runs the same measurements under both.

```sh
go test -tags ncruces ./...
```

`mattn/go-sqlite3` is not wired up. It needs CGO, which ADR-1 rules out for the shipped
binary, so a number for it would measure something that cannot be adopted.

**The comparison at 500k files, p50 over three runs each:**

| Measurement | `modernc.org/sqlite` | `ncruces/go-sqlite3` |
|---|---|---|
| Library list page | 13.3, 13.5, 13.3 ms | 25.3, 26.4, 25.2 ms |
| Title lookup | 0.8 ms | 0.8 ms |
| Import batch commit | 13.1, 6.8, 7.9 ms | 6.5, 11.4, 12.0 ms |
| Interactive write behind bulk | 8.0, 6.4, 0.04 ms | 0.04, 6.4, 0.04 ms |

**modernc stays.** The list page is twice as fast on it and the figure barely moves between
runs; every write figure overlaps between the two, so nothing there separates them. Reads
are what somebody is waiting for. The build tag stays as well, so the question is one
command away again when the workload changes at stage 5.

## Where the targets stand

Measured on the development machine at 500,000 files, `modernc.org/sqlite`, in Docker:

| Measurement | p50 | p99 | Target |
|---|---|---|---|
| Library list page (cursor, filtered) | 13 ms | 24 ms | 100 ms |
| Title lookup | 0.8 ms | 32 ms | 150 ms |
| Import batch commit (200 rows) | 13 ms | 15 ms | 50 ms |
| Interactive write behind bulk | 6 ms | 30 ms | 25 ms — **not met** |

The last one is the only target outstanding, and the cause is below. Its test guards the
measured level rather than the target, so a regression still fails the build.

## What the measurements have found

Recorded so the next person does not re-derive them.

**An insert that returns its row costs about four times one that does not.** Measured over
100 `media_object` inserts in one transaction, on both drivers:

| Statement | p50 |
|---|---|
| 3 columns, no `RETURNING` | ~2.6 ms |
| 20 columns, no `RETURNING` | ~3.8 ms |
| 20 columns, `RETURNING id` | ~13 ms |
| 20 columns, `RETURNING` every column | ~16 ms |

The column count is nearly free; the row that comes back is not. `RETURNING` turns an exec
into a query, and `database/sql` pays for a result set per row. Bulk insert paths therefore
use `:execlastid` rather than `:one` — see `documentation/sql-and-sqlc.md`.

**Preparing statements once per transaction saves about 30%**, which is real but far
smaller. ADR-28's prepared-statement cache is worth having; it is not the main cost.

**The interactive write's tail is the WAL auto-checkpoint.** SQLite checkpoints when the
WAL passes 1000 pages, and the transaction that happens to cross the threshold pays for
it. Under a sustained import that is sometimes the write a user is waiting on. Measured
over 500 interactive writes with bulk import running throughout:

| `wal_autocheckpoint` | p50 | p95 | p99 | max |
|---|---|---|---|---|
| 1000 (the SQLite default) | 6.3 ms | 16.1 ms | 31.5 ms | 37.5 ms |
| 0 (checkpointing suppressed) | 7.3 ms | 15.7 ms | 18.4 ms | 21.8 ms |

Suppressing it alone is not the fix — the WAL would then grow without bound. Checkpointing
has to become scheduled work: `wal_autocheckpoint=0` plus a periodic PASSIVE checkpoint
submitted through the write queue at bulk priority, so it is ordered like every other write
and never lands inside somebody's request. That is not built yet.
