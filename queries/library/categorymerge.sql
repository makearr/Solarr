-- name: ListMergeDuplicates :many
SELECT d.id AS item_id, held.id AS counterpart_id, d.work_id
FROM desired_item d
JOIN desired_item held ON held.category_id = sqlc.arg(into_category_id) AND held.work_id = d.work_id AND held.media_ref = d.media_ref
 AND COALESCE(held.edition_id, 0) = COALESCE(d.edition_id, 0)
WHERE d.id IN (SELECT value FROM json_each(sqlc.arg(item_ids)));

-- name: DeleteDesiredItemRow :exec
DELETE FROM desired_item WHERE id = ?;

-- name: MoveDesiredItemsToCategory :exec
UPDATE OR IGNORE desired_item SET category_id = sqlc.arg(into_category_id), dirty = 1 WHERE id IN (SELECT value FROM json_each(sqlc.arg(item_ids)));

-- name: ListWorksOfItems :many
SELECT DISTINCT work_id FROM desired_item WHERE category_id = sqlc.arg(category_id) AND id IN (SELECT value FROM json_each(sqlc.arg(item_ids)));

-- name: NextCategoryItemIDs :many
SELECT id FROM desired_item WHERE category_id = ? AND id > ? ORDER BY id LIMIT ?;

-- name: DropCategoryDesiredItems :exec
DELETE FROM desired_item WHERE category_id = ?;

-- name: MoveRequestsToCategory :exec
UPDATE OR IGNORE request SET category_id = sqlc.arg(into_category_id) WHERE category_id = sqlc.arg(from_category_id);
-- name: DropCategoryRequests :exec
DELETE FROM request WHERE category_id = ?;
-- name: MoveCollectionsToCategory :exec
UPDATE OR IGNORE collection SET category_id = sqlc.arg(into_category_id) WHERE category_id = sqlc.arg(from_category_id);
-- name: DropCategoryCollections :exec
DELETE FROM collection WHERE category_id = ?;
-- name: MoveImportSessionsToCategory :exec
UPDATE OR IGNORE import_session SET category_id = sqlc.arg(into_category_id) WHERE category_id = sqlc.arg(from_category_id);
-- name: DropCategoryImportSessions :exec
DELETE FROM import_session WHERE category_id = ?;
-- name: MoveMoveJobsToCategory :exec
UPDATE OR IGNORE move_job SET category_id = sqlc.arg(into_category_id) WHERE category_id = sqlc.arg(from_category_id);
-- name: DropCategoryMoveJobs :exec
DELETE FROM move_job WHERE category_id = ?;
-- name: MoveSubscriptionsToCategory :exec
UPDATE OR IGNORE subscription SET category_id = sqlc.arg(into_category_id) WHERE category_id = sqlc.arg(from_category_id);
-- name: DropCategorySubscriptions :exec
DELETE FROM subscription WHERE category_id = ?;
-- name: DropCategoryPeople :exec
DELETE FROM person_library WHERE category_id = ?;
-- name: DropCategoryOrganisations :exec
DELETE FROM organisation_library WHERE category_id = ?;
