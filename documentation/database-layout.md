# Database layout

Four persistent databases, one disposable cache (ADR-2, revised 2026-10-04 by `statistics.db`). Each has one writer and a read pool
(ADR-3), and **no query joins across them** — compose in Go (architecture section 4.3).

| Database | Holds | Shape |
|---|---|---|
| `settings.db` | Configuration and credentials | Small, rarely written, **fully cached in memory** |
| `library.db` | Everything that scales with the library | Large, written constantly, never cached whole |
| `jobs.db` | The work queue and the append-only event log | Churns; retention-bounded |
| `statistics.db` | What was seen and done: download history, and (moving there) candidates, decisions and rollups | Append-mostly; **`library.db` is correct with it empty**: nothing that decides what to download, import, replace or delete reads it |

Whether an item is fresh because RSS showed it (ADR-55) is `rss_sighting` in `library.db`, written when a decision
on an RSS-found release is recorded; the planner and the Fresh filter read it, not the candidates.

`statistics.db` holds download history, the statistics rollup, and every release a search found (`candidate`) with
what was decided about it (`candidate_decision`, `decision_rollup`), copied there from `library.db` with their ids.
The planner, dispatch and Wanted never read them: a held release and an acquisition carry their own release, and
freshness reads `rss_sighting`. Interactive search and a person's send read the decisions from `statistics.db`;
`library.db`'s old candidate tables are no longer written and are dropped a release later.

Before revision 2 the paragraph below was true. There is no `history.db`. `decision_rollup` sat in `library.db` beside the decisions it
aggregates, so F1's "why wasn't this downloaded?" stays one join across both the recent and the
aged record. `event_log` sits in `jobs.db`, whose profile it shares: high churn,
retention-bounded, and not authoritative for library correctness. Under ADR-3's one writer per
file, putting the event log in `library.db` would queue every append against a library commit,
and an event is lossy notification data (ADR-9) that must never block one.

Download history is `statistics.db`'s `history` table: one row per grab, import, failure and removal (taken out of
the queue by a person), with the library, work, acquisition, release, indexer and client, and the work's title and
lead as they were then, so a row outlives its work. Activity → History and a work's History tab read it, newest
first by id; the API filters on the library. It used to be `event_log` rows in `jobs.db`, which is pruned; the first
start after the move copies them over once (`event_id` remembers each, so an interrupted copy resumes).
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
subscriptions, collections, `artwork` and learned reputation. The statistics page's figures (`stat_rollup`, and the
discography coverage in it) are measured from `library.db` by the statistics job and stored in `statistics.db`.

People, credits, tags, collections, counters and reputation are **library data, not settings**.
`credit` alone runs to hundreds of thousands of rows at the ADR-22 design scale, and `counter`
is written on every state transition — neither survives the "fully cached in memory" property
that makes `settings.db` what it is.

`person_library` and `organisation_library` say which libraries a person or organisation appears in, by role
(role `''` is any role, each work counted once): the one People list of every media type reads them, and so
does the rule that decides whether a viewer may see someone. A work belongs to a library through its
`desired_item` rows. Both are rewritten for the people concerned in the same transaction as the change that
moves them: a credit, a work's first wanted item in a library, a work's removal, a merge, a network link
(`internal/db/library/peoplerollup.go`). A library set to add discovered albums as `none` therefore lists
no artist for files it does not want. An album's label is a `work_organisation` row (role `label`, kind `label`,
id `musicbrainz:label`); the `work_field` `label` from provider `musicbrainz` records that the album was asked, its
value the label or NULL for none, so each album is asked once.

`work_field` keeps one row per (work, field, provider). Which value a reader sees is one rule,
`internal/metadata/metaresolve`: a person's own (`__manual__`) wins, otherwise the newest non-empty value. A media type
applies its provider priority when it fetches and merges its sources, so what it stored last is its current answer;
the list's SQL orders by the same rule (`metaresolve.OrderSQL`).

## What the probe read

`media_probe` keeps each file's full probe (`json`) and its `probe_version`: 1 read the streams, 2 also the lyrics
language, 3 also a video's picture, subtitle tracks and encoder's CRF (`library.ProbeVersion*`). A file probed
before what the probe reads now is found by number (`probe_version < 2` for any file, `< 3` for a video) and read
once more by the background probe. `media_object.probe_json` is no longer written and goes in a later migration.

`media_object.kind` is what the file is by its extension -- `audio`, `video`, `subtitle`, `image` or `other` -- set
when the file is registered, from the one table in `internal/platform/filekind` that the scan, the import classifier
and the TV consumer all read. "A video" above is `kind = 'video'`. Rows registered before the column (migration 0071)
are classified once at startup.

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
`subscription.profile_id`, `subscription.user_id`, `import_list_entry.list_id`, `import_list_state.list_id`.

Deleting a referenced row is **refused, not cascaded** (Invariant 41). The references are
counted first and reported. The one exception is an import list's own record of what it did: deleting the list forgets
it (`library.ImportLists.Forget`), as it describes nothing but that list.

## Connections and page cache

Each database has one write connection, a read pool and one checkpointer. SQLite's
`cache_size` is **per connection**, so a figure meant for the library, applied to all three
databases, is a ceiling several times what it looks like: 64 MiB across six connections
each was over a gigabyte on hardware that is often a NAS.

`database.cache_size_kb` in `config.yml` therefore sizes `library.db`, the only one that
grows; Settings → Cache edits it and reports each database's cache use and hit ratio (SQLite's
`sqlite3_db_status` counters, read on request, nothing on the query path; modernc builds only). The others take their size and pool from what they hold, in
`cmd/solarr/app/serve.go`:

| Database | Read pool | Page cache | Why |
|---|---|---|---|
| `library.db` | 4 | `cache_size_kb` (64 MiB) | The only one at ADR-22 scale |
| `jobs.db` | 2 | 8 MiB | Small, hot, never queried across 500k rows |
| `statistics.db` | 2 | 8 MiB | Append-mostly; read by History and the statistics |
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

Each directory starts with **one squashed baseline**, `NNNN_baseline.sql`: the schema and seed rows
every migration up to `NNNN` built, so a fresh database runs one file (plus anything newer). Its
`-- migrate:baseline replaces=<sha256>` line names the checksum of the last migration it absorbed:

| The database has applied | What happens |
|---|---|
| nothing | the baseline runs |
| version `NNNN` (the original, or the baseline itself) | already there; later migrations run |
| only versions below `NNNN` | refused (`store.ErrPredatesBaseline`): upgrade through a release that still ships them |

To squash again: build a database from the current chain, dump `sqlite_master` (shadow tables of
FTS5 excluded) and the seed rows into a new baseline at the last version, delete the older files,
and check that a database built from the baseline is identical (schema text, `table_xinfo`, foreign
keys, rows). The original files stay in git history. The last squash (2026-10-22) did this in
Python's `sqlite3`.

Every migration is preceded by a backup, and a failure restores from it rather than leaving a
half-applied schema (Invariant 7).

## Maintenance and backups

Both are scheduler tasks (Settings -> Scheduler, Maintenance), since 2026-09-24:

| Task | Default | What |
|---|---|---|
| Database maintenance | Sundays 04:00 | Per database in turn: `integrity_check`, `optimize`, `VACUUM`, `wal_checkpoint(TRUNCATE)` |
| Backup | daily 01:00 | A rotation backup of `library.db`, `jobs.db` and `statistics.db` |
| Clean old backups | daily 01:30 | Keeps the newest N (default 7) or the last N days; never fewer than 3 |
| Clean recycling bin | daily 02:30 | Purges recycle bin entries past their library's retention ([file-management.md](file-management.md)) |
| Clean old search results | daily 03:10 | Decisions older than `retention.decisions_full` (90 days) are counted into `decision_rollup` per item, day and outcome, then deleted. A release past its expiry is deleted once no decision refers to it; a held download carries its own copy of the release. At most 250 pages of 200 a run, so an old backlog is worked off over several nights |

Every task can also be set to run **at startup** (Settings -> Scheduler, "Add at startup"). Database maintenance and
backup are heavy: their startup run waits five minutes and is skipped when the task last ran within its own
interval, so a restart two days after the weekly maintenance does not repeat it.

**Maintenance holds the writer exclusively** (`store.Maintain`), so every write to that database
waits until it finishes; the others keep working. A failed integrity check stops that database's
pass before `VACUUM`, which would otherwise rebuild it from whatever it could still read.
`VACUUM` runs before the truncating checkpoint because in WAL mode its rewrite lands in the log.

`settings.db` is backed up on every change instead and pruned by `backup.keep_settings`. A value a sweep measured
(root capacity, the cache measurement, placement affinity) is not a change in this sense and takes no backup, or it
would push every real one out of the rotation. Backups are written `0600`, as the live file is.
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
