# Benchmarks

ADR-28 states performance as numbers, and numbers only mean something against a corpus.
`internal/db/library/fixture` builds one; `internal/db/library/bench_test.go` measures against it.

## The corpus

`fixture.Ensure` generates a `library.db` and caches it under
`internal/db/library/testdata/fixtures/`, named for its spec. Generation is deterministic —
same spec, same rows — so two runs, or two drivers, compare like with like. Nothing in
that directory is committed (`*.db` is gitignored); delete it to force a rebuild.

The name carries the spec, the generator version **and a fingerprint of the library
migrations**, so a schema change builds a new corpus rather than measuring the old one
against new queries. Superseded files are left behind rather than deleted — they are
large, and deleting a file this code did not create is not its business.

The mix follows the real library: roughly 50% music, 35% TV, 15% film, with music and TV
works carrying parts and a film satisfied by one file. Three details are deliberate:

- **Sort titles repeat.** Every eighth work shares one title, so a page boundary lands
  inside a group of equal sort keys. A cursor without its id tie-break drops rows there,
  and that has already happened once (notes section 11).
- **A tenth of the files are unfingerprinted**, so ADR-35's resumable cursor has work to
  find at any scale.
- **Timestamps come from a fixed epoch**, never the clock.

Scale is `SOLARR_BENCH_FILES`, defaulting to 20,000 files so an ordinary `make test`
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

**Full-text search** is `title_search` (migration 0010), an FTS5 index over titles and
aliases maintained by triggers. `TestADR28_SearchStaysUnderItsTarget` measures
`library.Search.Works` with a one-word, prefix-matched query — what a search box holds
partway through typing — and gets p99 17ms against the 150ms target at 500k, replacing
the indexed-title-lookup stand-in this section used to describe.

## The driver (ADR-5)

The driver is named in one place, behind a build tag: `internal/db/store/driver_modernc.go`
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
| Library list page (cursor, filtered) | 1.7 ms | 3.5 ms | 100 ms |
| Title lookup | 0.5 ms | 15 ms | 150 ms |
| Import batch commit (200 rows) | 3.7 ms | 5.7 ms | 50 ms |
| Interactive write behind bulk | 4.3 ms | 19 ms | 25 ms, under a writer that never pauses |

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

**An optional predicate in static SQL costs the index entirely.** The list page was
`SCAN work` plus a sort of the whole table, for every page, and passed its ADR-28 target
only because `work` holds 52k rows rather than 500k. Composing the WHERE clause from the
filters actually present took p50 from 13.4ms to 1.7ms and p99 from 23.5ms to 3.5ms at
500k. The full numbers and the rule are in `documentation/sql-and-sqlc.md`.

Finding it also turned up a missing index: `work_type_sort` leads on `media_type`, so the
**unfiltered** list -- the first thing anyone sees -- could not use it for ordering and
sorted the table regardless of how the filters were written. `work_sort` (migration 0008)
covers that, and `TestADR28_ListPlansReachAnIndexAndNeverSort` holds every filter
combination to an index and to no temp b-tree.

**Preparing statements once per transaction saves about 30%**, which is real but far
smaller. ADR-28's prepared-statement cache is worth having; it is not the main cost.

**The interactive write's tail was the WAL auto-checkpoint.** SQLite checkpoints when the
WAL passes 1000 pages, and the transaction that happens to cross the threshold pays for
it. Under a sustained import that is sometimes the write a user is waiting on. Measured
over 500 interactive writes with bulk import running throughout:

| `wal_autocheckpoint` | p50 | p95 | p99 | max |
|---|---|---|---|---|
| 1000 (the SQLite default) | 6.3 ms | 16.1 ms | 31.5 ms | 37.5 ms |
| 0 (checkpointing suppressed) | 7.3 ms | 15.7 ms | 18.4 ms | 21.8 ms |

Suppressing it alone is not a fix: the log then grows for as long as writing continues.
Four designs were measured against both halves of the problem, 800 bulk commits of 200
rows each with interactive writes interleaved:

| Design | Log after 800 commits | Interactive p99 |
|---|---|---|
| SQLite's own automatic checkpoint | ~4 MB | 30 ms |
| No checkpointing at all | 21 MB, still climbing | 18.4 ms |
| Checkpoint on the writer only | 1.7 MB | 25-28 ms |
| Beside the writer, `RESTART` when large | 18.7 MB, still climbing | 18.7-20.2 ms |
| **Beside the writer, writer rewinds when large** | **2.2 MB** | **23.5-26 ms** |

The last is what `internal/db/store/checkpoint.go` does. Copying frames out is the common
case and runs on its own connection beside the writer, where it costs nobody anything —
checkpointing takes the checkpointer lock, not the writer lock. But a checkpoint on a
second connection cannot rewind the file: SQLite does that when a transaction begins
against a fully checkpointed log, which a writer that never pauses never offers, and
`RESTART` and `TRUNCATE` both wait out readers and never catch up either. So when the log
outgrows `LogSizeLimitBytes` the writer checkpoints on its own connection, and its next
transaction starts the file again.

**Bounding the log costs about 5ms of p99, and there is no way around that with one
writer.** The remaining tail is that checkpoint plus the bulk transaction in front of it.
ADR-28's 25ms is stated for a workload that pauses to read, hash and probe between
batches; the benchmark's writer never pauses, so its test guards 30ms and says so.

## Scan batch writes (`library.Physical`, `library.Identity`)

Measured 2026-09-03 against the real 500k-file fixture, one query per file inside a single
transaction (no bulk SQL), 200 files per batch matching `internal/scanning/scan`'s own batch size:

| Batch | p50 | p99 | Limit |
|---|---|---|---|
| Register 200 new files (dedupe lookup misses, insert) | 12.2 ms | 17.0 ms | 50 ms |
| Register 200 already-known paths (dedupe lookup hits) | 2.2 ms | 3.1 ms | 50 ms |
| Resolve 200 links, all naming a new work (create + link) | 22.6 ms | 31.0 ms | 50 ms |

All three sit well inside the ADR-3 bulk-write budget even in each measurement's worst
case — every file new, or every link naming a work nothing has seen before, which a real
scan rarely does (most files in a batch share an album, so most links resolve to a work
the same transaction already created). No case here approached rewriting this as bulk SQL
or splitting the two transactions differently; the numbers did not ask for it.
`TestADR28_ScanRegisterBatch*` and `TestADR28_ScanResolveWorks*` in
`internal/db/library/bench_scan_test.go` hold these as tests, not just a report.
