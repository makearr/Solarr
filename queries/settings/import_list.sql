-- Per-library import lists (migration 0009, todo 4.16, ADR-38). Column
-- lists follow table order, or sqlc stops reusing models.

-- name: CreateImportList :one
INSERT INTO import_list (category_id, name, type, url, cadence_hours, monitor,
                          quality_profile_id, exclusions, enabled)
VALUES (sqlc.arg(category_id), sqlc.arg(name), sqlc.arg(type), sqlc.arg(url),
        sqlc.arg(cadence_hours), sqlc.arg(monitor), sqlc.arg(quality_profile_id),
        sqlc.arg(exclusions), sqlc.arg(enabled))
RETURNING id, category_id, name, type, url, cadence_hours, monitor,
          quality_profile_id, exclusions, enabled;

-- name: GetImportList :one
SELECT id, category_id, name, type, url, cadence_hours, monitor,
       quality_profile_id, exclusions, enabled
FROM import_list WHERE id = ?;

-- name: ListImportListsForCategory :many
SELECT id, category_id, name, type, url, cadence_hours, monitor,
       quality_profile_id, exclusions, enabled
FROM import_list WHERE category_id = ? ORDER BY name;

-- name: UpdateImportList :exec
UPDATE import_list SET name = ?, type = ?, url = ?, cadence_hours = ?, monitor = ?,
                        quality_profile_id = ?, exclusions = ?, enabled = ?
WHERE id = ?;

-- name: DeleteImportList :exec
DELETE FROM import_list WHERE id = ?;
