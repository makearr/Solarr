-- *Arr import runs (todo 4.13, ADR-52). Column lists follow table order.

-- name: CreateArrImport :one
INSERT INTO arr_import (instance_id, app, state, started_at)
VALUES (?, ?, ?, ?)
RETURNING id, instance_id, app, state, started_at, finished_at, error,
          artists_created, artists_matched, albums_created, albums_matched, items_parked;

-- name: FinishArrImport :exec
UPDATE arr_import
SET state = ?, finished_at = ?, error = ?,
    artists_created = ?, artists_matched = ?, albums_created = ?, albums_matched = ?, items_parked = ?
WHERE id = ?;

-- A run still RUNNING when the process starts was cut short by the restart: nothing is working on it any more.
-- name: AbandonRunningArrImports :execrows
UPDATE arr_import SET state = 'FAILED', finished_at = ?, error = 'interrupted by a restart; run it again, what was added is kept'
WHERE state = 'RUNNING';

-- name: SetArrImportState :exec
UPDATE arr_import SET state = ? WHERE id = ?;

-- name: GetArrImport :one
SELECT id, instance_id, app, state, started_at, finished_at, error,
       artists_created, artists_matched, albums_created, albums_matched, items_parked
FROM arr_import WHERE id = ?;

-- name: ListArrImportsForInstance :many
SELECT id, instance_id, app, state, started_at, finished_at, error,
       artists_created, artists_matched, albums_created, albums_matched, items_parked
FROM arr_import WHERE instance_id = ? ORDER BY started_at DESC;

-- name: AddArrImportItem :one
INSERT INTO arr_import_item (import_id, kind, source_id, title, person_id, person_created,
                             subscription_id, work_id, work_created,
                             desired_item_id, desired_item_created, payload_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, import_id, kind, source_id, title, person_id, person_created,
          subscription_id, work_id, work_created, desired_item_id, desired_item_created,
          payload_json, created_at;

-- name: ListArrImportItems :many
SELECT id, import_id, kind, source_id, title, person_id, person_created,
       subscription_id, work_id, work_created, desired_item_id, desired_item_created,
       payload_json, created_at
FROM arr_import_item WHERE import_id = ? ORDER BY id;

-- Revert's own lookup (library.ArrImportRevert): every item this run itself
-- created something for, the only rows a revert is allowed to touch.
-- name: ListArrImportItemsCreatedByRun :many
SELECT id, import_id, kind, source_id, title, person_id, person_created,
       subscription_id, work_id, work_created, desired_item_id, desired_item_created,
       payload_json, created_at
FROM arr_import_item
WHERE import_id = ? AND (person_created = 1 OR work_created = 1)
ORDER BY id;
