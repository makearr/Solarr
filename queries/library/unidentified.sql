-- F29: the manual identification queue (migration 0014). Column lists follow table
-- order, or sqlc stops reusing models.

-- name: UpsertUnidentified :exec
INSERT INTO identification_queue (media_object_id, root_folder_id, rel_path, candidates_json,
                                   first_seen_at, last_seen_at)
VALUES (sqlc.arg(media_object_id), sqlc.arg(root_folder_id), sqlc.arg(rel_path),
        sqlc.arg(candidates_json), sqlc.arg(first_seen_at), sqlc.arg(last_seen_at))
ON CONFLICT (media_object_id) DO UPDATE SET
  root_folder_id = excluded.root_folder_id, rel_path = excluded.rel_path,
  candidates_json = excluded.candidates_json, last_seen_at = excluded.last_seen_at;

-- A file that resolves on a later sweep leaves the queue entirely -- there is no
-- "resolved" state to keep around.
-- name: DeleteUnidentified :exec
DELETE FROM identification_queue WHERE media_object_id = ?;

-- One entry, for the manual resolve action -- confirming a candidate needs the
-- file's own root_folder_id (to link work_root_folder), which the ranked
-- candidates list alone does not carry.
-- name: GetUnidentified :one
SELECT media_object_id, root_folder_id, rel_path, candidates_json, first_seen_at, last_seen_at
FROM identification_queue WHERE media_object_id = ?;

-- filter_root_folders=0 means unfiltered; =1 filters to root_folder_ids, which is
-- empty when the caller can see nothing -- same ADR-27 shape as search.sql's
-- SearchWorks, json_each passing a Go slice as a JSON array.
-- name: ListUnidentified :many
SELECT media_object_id, root_folder_id, rel_path, candidates_json, first_seen_at, last_seen_at
FROM identification_queue
WHERE (sqlc.arg(filter_root_folders) = 0
       OR root_folder_id IN (SELECT value FROM json_each(sqlc.arg(root_folder_ids))))
  AND (first_seen_at > sqlc.arg(after_seen)
       OR (first_seen_at = sqlc.arg(after_seen) AND media_object_id > sqlc.arg(after_id)))
ORDER BY first_seen_at, media_object_id
LIMIT sqlc.arg(page_size);
