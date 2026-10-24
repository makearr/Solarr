# Configuration export and import (F9)

`GET /api/v1/settings/export` renders configuration as YAML; `POST /api/v1/settings/import`
applies it.

## What the document carries

Policy, and nothing else. Accounts, password hashes and API keys are absent: an export is
attached to bug reports and committed to repositories, so anything in it ends up somewhere
public.

Identifiers are absent too. Rows are matched on natural keys — a category by media type and
name, a root folder by path — so a document from one install applies to another, where the
numbers would mean different things.

The `settings` block carries every registered scalar setting as one flat group, always written
whole on export. On import **only the keys a document names change**; the rest keep their stored
values, so a hand-written block of one key cannot switch sign-in off. Keys that only mean
something together -- the network group, the cache budget, the priority weights -- are asked for
by name when a document names some of them but not all. `import_transfer_ladder` absent keeps the
stored order. A non-empty `network_allow_cidrs` is checked against the importing request's own
remote address the same way `PATCH /network` is (Invariant 89) -- an import with no real caller
address to check refuses conservatively rather than risk a lockout it cannot verify.

The Backups page downloads the document and restores one (2026-09-24).

A further media type of a library carries `library_of: <media_type>/<name>` of the library it belongs to; import
links it again. A category's `naming` block is present only once the library's naming has been saved; absent,
the library uses its media type's default. On import it is checked by the media type's own
naming scheme (every slot, every token), the same check a save through the interface gets.

## Stable output

Ordering is by natural key, not by id, so exporting twice produces an identical file and a diff
shows only what changed. An export that reshuffles itself is unusable in version control, which
is most of the point.

## All or nothing

An import is arbitrary input — a hand-edited file, an export from a newer version, something
from a forum post. It is validated whole and every problem is reported at once; reporting one
error at a time turns a five-mistake file into five round trips.

Nothing is written unless all of it is valid. A partial import leaves settings in a state the
user never chose and cannot easily undo (architecture section 14.4).

A key the format does not have is the one exception: it is not a problem but a **warning**. What
is known is applied, and the response lists every unknown key with its path and the nearest
valid one (answered 2026-09-24). Silence was the worst option -- a `root_folders:` nested under a
category once imported with a 204 and attached nothing.

`schedules` holds each scheduler task's *changed* cadences, enabled flag and run-at-startup, by
task key; a task absent keeps its defaults, and a field absent keeps what is stored. It exports
what was changed rather than every task because `settings.db` does not know which tasks exist.
`import_extra_files` and `import_extra_file_extensions` are in `settings`; absent keeps them.
A category's `file_management` is present only once saved; absent keeps Sonarr's defaults.
The backup retention (`backup_retention`, `backup_keep_count`, `backup_keep_days`) is in
`settings`; absent `backup_retention` leaves all three alone.

## Merge, not replace

Applying a document twice changes nothing the second time — it is how a configuration is kept in
sync. Replacing would mean deleting categories that hold a library to satisfy a file.

## Error framing

`config.Problems` describes both a bad `config.yml` at startup and a bad imported document.
Those are not the same event, so `Problems.Report` takes the caller's own heading and closing
line — telling someone importing through the web interface that "config.yml has 4 errors" and
that "nothing has been started" names a file they did not touch and a process already running.
