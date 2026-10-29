-- Import list entries and state (migration 0099, ADR-38). Column lists follow table order.

-- name: ListImportListEntries :many
SELECT list_id, provider, external_id, outcome, work_id, title, seen_at
FROM import_list_entry WHERE list_id = ? ORDER BY provider, external_id;

-- name: RecordImportListEntry :exec
INSERT INTO import_list_entry (list_id, provider, external_id, outcome, work_id, title, seen_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (list_id, provider, external_id) DO UPDATE SET
  outcome = excluded.outcome, work_id = excluded.work_id, title = excluded.title, seen_at = excluded.seen_at;

-- name: GetImportListState :one
SELECT list_id, previewed_options, previewed_at, run_at, run_added, run_failure
FROM import_list_state WHERE list_id = ?;

-- name: RecordImportListPreview :exec
INSERT INTO import_list_state (list_id, previewed_options, previewed_at)
VALUES (?, ?, ?)
ON CONFLICT (list_id) DO UPDATE SET
  previewed_options = excluded.previewed_options, previewed_at = excluded.previewed_at;

-- name: RecordImportListRun :exec
INSERT INTO import_list_state (list_id, run_at, run_added, run_failure)
VALUES (?, ?, ?, ?)
ON CONFLICT (list_id) DO UPDATE SET
  run_at = excluded.run_at, run_added = excluded.run_added, run_failure = excluded.run_failure;

-- name: DeleteImportListEntries :exec
DELETE FROM import_list_entry WHERE list_id = ?;

-- name: DeleteImportListState :exec
DELETE FROM import_list_state WHERE list_id = ?;
