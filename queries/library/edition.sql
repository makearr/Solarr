-- name: CountEditionFiles :one
SELECT COUNT(*) FROM desired_item_file f JOIN desired_item d ON d.id = f.desired_item_id WHERE d.edition_id = ?;

-- A wanted item of the old work stops pointing at an edition that is no longer its work's.
-- name: ClearEditionOfOtherWorks :exec
UPDATE OR IGNORE desired_item SET edition_id = NULL WHERE edition_id = sqlc.arg(edition_id) AND work_id <> sqlc.arg(work_id);
