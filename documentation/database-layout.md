# Database layout

Three persistent databases, one disposable cache (ADR-2). Each has one writer and a read pool
(ADR-3), and **no query joins across them** — compose in Go (architecture section 4.3).

| Database | Holds | Shape |
|---|---|---|
| `settings.db` | Configuration and credentials | Small, rarely written, **fully cached in memory** |
| `library.db` | Everything that scales with the library | Large, written constantly, never cached whole |
| `jobs.db` | The work queue and the append-only event log | Churns; retention-bounded |

There is no `history.db`. `decision_rollup` sits in `library.db` beside the decisions it
aggregates, so F1's "why wasn't this downloaded?" stays one join across both the recent and the
aged record. `event_log` sits in `jobs.db`, whose profile it shares: high churn,
retention-bounded, and not authoritative for library correctness. Under ADR-3's one writer per
file, putting the event log in `library.db` would queue every append against a library commit,
and an event is lossy notification data (ADR-9) that must never block one.

Download history is `event_log` rows of kind `download.grabbed`, `download.imported`,
`download.failed` and `download.removed` (taken out of the queue by a person), subject `work`, with `download.HistoryData` as the payload: album, artist,
release, indexer and client as they were at the time. Activity → History and an album's page
read them; the library each belongs to is in the payload, and the API filters on it.
Activity → Queue is not stored: `GET /queue` joins the in-flight `acquisition` rows to each
download client's live answer on every request, as Lidarr's queue does.

## Which database a table belongs in

The test is **what the row count scales with**, not what the table is about.

A table belongs in `settings.db` only if all three hold:

1. It is configuration or a credential — something a person set.
2. Its size is bounded by how much the operator configured, not by how much media they own.
3. Loading every row into memory on every reload is reasonable.

Everything else is `library.db`. Applying that test:

**`settings.db`** — `app_user`, `user_category_access`, `user_preference`, `api_key`,
`root_folder`, `category`, `category_root_folder`, `placement_affinity`, `quality_profile`,
`naming_profile`, `naming_format`, `consumer_profile`, `language_profile`, `indexer`, `downloader`,
`metadata_provider`, `discovery_job`, `plugin_state`.

**`library.db`** — identity (`work`, `work_part`, `edition`, titles, external ids), files (`media_object`,
`library_path`, `media_attributes`), desired state (`desired_item`, `desired_item_file`,
`hunt_queue`, `counter`), acquisition (`candidate`, `acquisition`, `blocklisted_release`,
`recycleBin`), people and organisations (`person`, `credit`, `organisation`), tags,
subscriptions, collections, `artwork`, `stat_rollup` and learned reputation.

People, credits, tags, collections, counters and reputation are **library data, not settings**.
`credit` alone runs to hundreds of thousands of rows at the ADR-22 design scale, and `counter`
is written on every state transition — neither survives the "fully cached in memory" property
that makes `settings.db` what it is.

## Interned attributes against per-file measurements

`media_attributes` is interned: one row per distinct combination, referenced by many files.
That only works while every column in it is categorical and low-cardinality -- resolution,
source, codecs, hdr, channels, container, scan type, sample rate, frame rate, lossless. A few
thousand combinations cover 500k files, and "all my 2160p files" stays a small-table scan.

Measurements that are unique per file -- `duration_ms`, `width`, `height` and the three
bitrates -- are columns on `media_object` instead. Interning them would defeat the point:
duration alone is near-unique, so the table would grow one row per file and every attribute
read would become a join for nothing.

The division is not claimed-against-measured. `media_object` carries **both**
`claimed_attributes_id` and `measured_attributes_id` into the same interned table, so a release
name's claim and ffprobe's finding are two rows of one shape and ADR-39 compares them field by
field. A release name states no duration and no exact pixel dimensions, so the columns that
moved had no claimed counterpart to be compared against anyway.

## Crossing the boundary

`library.db` references `settings.db` rows by integer id and **carries no foreign key** — a
foreign key cannot cross a database file, and ADR-25 puts the integrity check in the
application instead.

The columns that do this: `work.added_by`, `library_path.root_folder_id`,
`desired_item.category_id`, `desired_item.profile_id`, `desired_item.added_by`,
`candidate.indexer_id`, `recycleBin.root_folder_id`, `subscription.category_id`,
`subscription.profile_id`, `subscription.user_id`.

Deleting a referenced row is **refused, not cascaded** (Invariant 41). The references are
counted first and reported.

## Connections and page cache

Each database has one write connection, a read pool and one checkpointer. SQLite's
`cache_size` is **per connection**, so a figure meant for the library, applied to all three
databases, is a ceiling several times what it looks like: 64 MiB across six connections
each was over a gigabyte on hardware that is often a NAS.

`database.cache_size_kb` in `config.yml` therefore sizes `library.db`, the only one that
grows. The others take their size and pool from what they hold, in
`cmd/solarr/app/serve.go`:

| Database | Read pool | Page cache | Why |
|---|---|---|---|
| `library.db` | 4 | `cache_size_kb` (64 MiB) | The only one at ADR-22 scale |
| `jobs.db` | 2 | 8 MiB | Small, hot, never queried across 500k rows |
| `settings.db` | 1 | 2 MiB | Fully cached in Go already (ADR-25) |
| any checkpointer | — | 2 MiB | Walks the log; a reader's cache would be idle |

That is a ceiling of about 354 MiB, and `TestDatabaseProfiles_KeepTheCacheCeilingInHand`
fails if a pool or a size pushes it past 384 MiB.

**The driver is not configurable.** It is chosen by a build tag (`internal/db/store/
driver_modernc.go` and `driver_ncruces.go`, ADR-5) and reported at startup and in
`/api/v1/about`. `config.yml` naming one would be a setting that decides nothing.

## File permissions

Database files are created `0600` before the driver can create them, so they never exist
world-readable. The write-ahead log and shared-memory files inherit the mode, which is the part
that matters — they carry the same pages, so a restricted database beside a world-readable WAL
protects nothing.

## Migrations

One directory per database under `migrations/`, files named `NNNN_description.sql`, applied in
version order with a checksum per migration. An edited migration that has already been applied
is a hard failure naming both checksums.

Every migration is preceded by a backup, and a failure restores from it rather than leaving a
half-applied schema (Invariant 7).

## Maintenance and backups

Both are scheduler tasks (Settings -> Scheduler, Maintenance), since 2026-09-24:

| Task | Default | What |
|---|---|---|
| Database maintenance | Sundays 04:00 | Per database in turn: `integrity_check`, `optimize`, `VACUUM`, `wal_checkpoint(TRUNCATE)` |
| Backup | daily 01:00 | A rotation backup of `library.db` and `jobs.db` |
| Clean old backups | daily 01:30 | Keeps the newest N (default 7) or the last N days; never fewer than 3 |
| Clean recycling bin | daily 02:30 | Purges recycle bin entries past their library's retention ([file-management.md](file-management.md)) |

**Maintenance holds the writer exclusively** (`store.Maintain`), so every write to that database
waits until it finishes; the others keep working. A failed integrity check stops that database's
pass before `VACUUM`, which would otherwise rebuild it from whatever it could still read.
`VACUUM` runs before the truncating checkpoint because in WAL mode its rewrite lands in the log.

`settings.db` is backed up on every change instead and pruned by `backup.keep_settings`.
`config.yml`'s `backup.schedule` and `backup.keep` are still accepted but ignored, with a warning.

## Corruption recovery at startup

Every database is verified before it is used (ADR-15, architecture section 15.1).
`store.OpenChecked` runs `PRAGMA quick_check`, confirms a failure with
`integrity_check` — `quick_check` has false positives — and only then moves the file
aside as `<name>.db.corrupt.<timestamp>` and restores backups newest first until one
checks out. If none does, the process refuses to start rather than running on data it
cannot vouch for. Nothing is ever deleted.

**The check runs before `Open`, on a connection of its own.** `Open` settles
`page_size` by querying `sqlite_schema`, so it fails on a corrupt file before any of
the three real handles exist; probing first means recovery never has to close a live
`*Store`. A database that is not there yet is a fresh install with nothing to check —
and creating it during the probe would beat the `0600` creation above to it.

**The log cannot be preserved with the damaged file.** The driver discards an
unreadable `-wal` when the probe opens the database — including on `SQLITE_NOTADB`,
where SQLite never reads a page — so by the time anything is moved aside there is no
log left to move. `Restore` still clears stale sidecars, because a user-initiated
restore runs with no probe in front of it.

## The library sweep

`internal/scanning/scan` is section 10.4's mtime sweep: at ADR-22's 500k files a full rescan is
not an answer, so only directories whose mtime has moved are read. The whole tree is
still traversed — a directory's mtime moves when its **own** entries change, never
when a grandchild's do — but traversal is stat calls and reading is the expensive
half.

**The watermark is when the last completed sweep started**, not when it finished and
not the newest mtime it saw. A sweep over 500k files takes minutes and the library is
live throughout; a directory written after the walk has already passed it would sit
behind a later watermark and never be read again. Starting-time errs toward
re-reading, which is recoverable, over missing a change, which is not.

**The resume cursor is compared segment by segment, never as a string.** The walk
visits a directory's whole subtree before its next sibling, so the order is `a`,
`a/c`, `a-b` — while as plain strings `a-b` < `a/c`, because `-` is below `/`. A
string comparison silently skips that sibling. The cursor also advances only *after*
the caller has committed a batch, because it means "everything up to here is durable".

### Progress is persisted per batch

`scan_state` (migration 0011) holds one row per root folder. **Only a completed sweep
moves the watermark**; a sweep that starts records only that it started, and one that
is interrupted leaves its cursor behind so a restart resumes rather than rescanning.
The cursor is written *after* the consumer has committed a batch — writing it first
means a crash between the two silently drops that batch.

A root folder is `settings.db` policy, referenced by id with no foreign key (ADR-25),
so the scan asks for it rather than reading the table. An offline root is a
**retryable** failure, never a conclusion: an unmounted disk must not look like the
loss of a library. An unconfigured root and an unreadable payload are terminal — they
will not become valid on a retry.

**A test must not mix a fake clock with the real filesystem here.** The sweep compares
the injected clock against filesystem mtimes, so a `clock.Fake` at epoch over real
files makes every directory look newer than every watermark. `internal/scanning/scan`'s job
tests walk an `fsx.Mem` on the same fake clock, with a real `library.db` beside it.

## Post-restore reconciliation

After any restore, and before workers start, `internal/db/recovery` fails every
unfinished job with a reason, marks every `desired_item` dirty so the planner
re-evaluates the whole library (ADR-6), reports the `settings.db` references the
restore orphaned — `library.db` holds no foreign key to it (ADR-25) — and writes all
of it to `event_log` under `database.restored`, which is what makes the notification
survive the restart that produced it.

It runs *after* migrations, where architecture section 15.1 puts it at step 3 before
them: it queries both databases at the current schema and needs `settings.db`'s
categories and root folders to find orphans, neither of which exists earlier. ADR-15
only requires that it precede workers, which it does.

What ADR-15 asks for that cannot be built yet is named in the report's `gaps`, not
stubbed: re-deriving in-flight acquisitions needs a downloader client (stage 5), and
checking `candidate.indexer_id` and `acquisition.downloader_id` needs those to be
configurable (stage 5).
