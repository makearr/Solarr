-- F29: the manual identification queue (migration 0014). Column lists follow table
-- order, or sqlc stops reusing models.

-- name: UpsertUnidentified :exec
-- A file marked "not media" is never queued again, nor one that already belongs to a work: a
-- person's resolve survives a full rescan the chain still cannot decide (found 2026-09-24). An
-- unverified album (only local ids, D6) does not count: its files still await identification.
INSERT INTO identification_queue (media_object_id, root_folder_id, rel_path, candidates_json,
                                   first_seen_at, last_seen_at)
SELECT sqlc.arg(media_object_id), sqlc.arg(root_folder_id), sqlc.arg(rel_path),
       sqlc.arg(candidates_json), sqlc.arg(first_seen_at), sqlc.arg(last_seen_at)
WHERE NOT EXISTS (SELECT 1 FROM not_media nm WHERE nm.media_object_id = sqlc.arg(media_object_id))
  AND NOT EXISTS (SELECT 1 FROM work_file wf WHERE wf.media_object_id = sqlc.arg(media_object_id)
                    AND EXISTS (SELECT 1 FROM work_external_id vx
                                WHERE vx.work_id = wf.work_id AND vx.provider NOT LIKE 'local:%'))
ON CONFLICT (media_object_id) DO UPDATE SET
  root_folder_id = excluded.root_folder_id, rel_path = excluded.rel_path,
  candidates_json = excluded.candidates_json, last_seen_at = excluded.last_seen_at;

-- A file that resolves on a later sweep leaves the queue entirely -- there is no
-- "resolved" state to keep around.
-- name: DeleteUnidentified :exec
DELETE FROM identification_queue WHERE media_object_id = ?;

-- A file that covers an episode or a track is identified, whoever placed it and whenever.
-- name: DeletePlacedUnidentified :exec
DELETE FROM identification_queue
WHERE EXISTS (SELECT 1 FROM desired_item_file f WHERE f.media_object_id = identification_queue.media_object_id);

-- One entry, for the manual resolve action -- confirming a candidate needs the
-- file's own root_folder_id (to link work_root_folder), which the ranked
-- candidates list alone does not carry.
-- name: GetUnidentified :one
SELECT media_object_id, root_folder_id, rel_path, candidates_json, first_seen_at, last_seen_at
FROM identification_queue WHERE media_object_id = ?;

-- Library health's own "unmatched files" figure (todo 4.8) -- same filter
-- shape as ListUnidentified below, a count rather than a page.
-- name: CountUnidentified :one
SELECT CAST(count(*) AS INTEGER) AS total
FROM identification_queue
WHERE sqlc.arg(filter_root_folders) = 0
   OR root_folder_id IN (SELECT value FROM json_each(sqlc.arg(root_folder_ids)));

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

-- name: MarkNotMedia :exec
INSERT INTO not_media (media_object_id, marked_at) VALUES (?, ?)
ON CONFLICT (media_object_id) DO NOTHING;

-- The queued files directly in one folder, for "identify the whole folder as".
-- name: ListUnidentifiedInFolder :many
SELECT media_object_id FROM identification_queue
WHERE root_folder_id = sqlc.arg(root_folder_id)
  AND substr(rel_path, 1, length(CAST(sqlc.arg(folder_prefix) AS TEXT))) = CAST(sqlc.arg(folder_prefix) AS TEXT)
  AND instr(substr(rel_path, length(CAST(sqlc.arg(folder_prefix) AS TEXT)) + 1), '/') = 0
ORDER BY media_object_id;

-- name: RecordManualIdentification :exec
INSERT INTO manual_identification (media_object_id, work_id, decided_at) VALUES (?, ?, ?)
ON CONFLICT (media_object_id) DO UPDATE SET work_id = excluded.work_id, decided_at = excluded.decided_at;

-- Which of a batch's files a person already identified; ids is a JSON array.
-- name: ListManuallyIdentified :many
SELECT media_object_id FROM manual_identification
WHERE media_object_id IN (SELECT value FROM json_each(CAST(sqlc.arg(media_object_ids) AS TEXT)))
ORDER BY media_object_id;
