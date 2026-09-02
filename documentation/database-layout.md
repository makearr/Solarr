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

## Which database a table belongs in

The test is **what the row count scales with**, not what the table is about.

A table belongs in `settings.db` only if all three hold:

1. It is configuration or a credential — something a person set.
2. Its size is bounded by how much the operator configured, not by how much media they own.
3. Loading every row into memory on every reload is reasonable.

Everything else is `library.db`. Applying that test:

**`settings.db`** — `app_user`, `user_category_access`, `user_preference`, `api_key`,
`root_folder`, `category`, `category_root_folder`, `placement_affinity`, `quality_profile`,
`naming_profile`, `consumer_profile`, `language_profile`, `indexer`, `downloader`,
`metadata_provider`, `discovery_job`, `plugin_state`.

**`library.db`** — identity (`work`, `work_part`, `edition`, titles, external ids), files (`media_object`,
`library_path`, `media_attributes`), desired state (`desired_item`, `desired_item_file`,
`hunt_queue`, `counter`), acquisition (`candidate`, `acquisition`, `blocklisted_release`,
`quarantine`), people and organisations (`person`, `credit`, `organisation`), tags,
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
`candidate.indexer_id`, `quarantine.root_folder_id`, `subscription.category_id`,
`subscription.profile_id`, `subscription.user_id`.

Deleting a referenced row is **refused, not cascaded** (Invariant 41). The references are
counted first and reported.

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
