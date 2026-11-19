-- name: CountPlacementAffinityForCategory :one
SELECT CAST(COUNT(*) AS INTEGER) FROM placement_affinity WHERE category_id = ?;

-- name: CountDownloadFoldersForCategory :one
SELECT CAST(COUNT(*) AS INTEGER) FROM download_folder WHERE category_id = ?;

-- name: CountArrInstancesForCategory :one
SELECT CAST(COUNT(*) AS INTEGER) FROM arr_instance WHERE category_id = ?;

-- name: CountImportListsForCategory :one
SELECT CAST(COUNT(*) AS INTEGER) FROM import_list WHERE category_id = ?;

-- name: CountArrInstancesClashing :one
SELECT CAST(COUNT(*) AS INTEGER) FROM arr_instance s
WHERE s.category_id = sqlc.arg(from_category_id)
  AND EXISTS (SELECT 1 FROM arr_instance t WHERE t.category_id = sqlc.arg(into_category_id) AND t.app = s.app);

-- name: DeleteExclusionPatternsForCategory :exec
DELETE FROM exclusion_pattern WHERE category_id = ?;

-- name: MoveCategoryRootFolders :exec
UPDATE OR IGNORE category_root_folder SET category_id = sqlc.arg(into_category_id) WHERE category_id = sqlc.arg(from_category_id);

-- name: DropCategoryRootFolders :exec
DELETE FROM category_root_folder WHERE category_id = ?;

-- name: DropCategoryAccess :exec
DELETE FROM user_category_access WHERE category_id = ?;

-- name: MoveImportLists :exec
UPDATE OR IGNORE import_list SET category_id = sqlc.arg(into_category_id) WHERE category_id = sqlc.arg(from_category_id);

-- name: RenameLeftoverImportLists :exec
UPDATE import_list SET category_id = sqlc.arg(into_category_id), name = name || ' (merged ' || id || ')'
WHERE category_id = sqlc.arg(from_category_id);

-- name: MoveDownloadFolders :exec
UPDATE download_folder SET category_id = sqlc.arg(into_category_id) WHERE category_id = sqlc.arg(from_category_id);

-- name: MoveArrInstances :exec
UPDATE arr_instance SET category_id = sqlc.arg(into_category_id) WHERE category_id = sqlc.arg(from_category_id);

-- name: MoveExclusionPatterns :exec
UPDATE OR IGNORE exclusion_pattern SET category_id = sqlc.arg(into_category_id) WHERE category_id = sqlc.arg(from_category_id);

-- name: MovePlacementAffinity :exec
UPDATE OR IGNORE placement_affinity SET category_id = sqlc.arg(into_category_id) WHERE category_id = sqlc.arg(from_category_id);

-- name: DropPlacementAffinity :exec
DELETE FROM placement_affinity WHERE category_id = ?;
