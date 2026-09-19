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

The `settings` block carries every todo-4.14-registered scalar setting (auth, import,
network, cache, priority — ADR-21, ADR-26) as one flat, always-populated group on export.
It is optional on import: a document that omits it changes nothing about those settings, so
a hand-edited file need not repeat them all just to add a category. `import_transfer_ladder`
follows the same rule one level down -- absent means "leave the stored order alone", since an
empty list would otherwise be indistinguishable from "place nothing". `import_rename_on_import`
is always written on export, like every other flag in the block; absent on import it keeps
whatever is stored. When present, a
non-empty `network_allow_cidrs` is checked against the importing request's own remote
address the same way `PATCH /network` already is (Invariant 89) — an import with no real
caller address to check (anything but the HTTP endpoint) refuses conservatively rather than
risk a lockout it cannot verify.

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

## Merge, not replace

Applying a document twice changes nothing the second time — it is how a configuration is kept in
sync. Replacing would mean deleting categories that hold a library to satisfy a file.

## Error framing

`config.Problems` describes both a bad `config.yml` at startup and a bad imported document.
Those are not the same event, so `Problems.Report` takes the caller's own heading and closing
line — telling someone importing through the web interface that "config.yml has 4 errors" and
that "nothing has been started" names a file they did not touch and a process already running.
