-- One naming profile per library (category.naming_profile_id), its templates per slot.

-- name: ListNamingProfiles :many
SELECT id, name, rename_files, replace_illegal_characters, colon_replacement
FROM naming_profile ORDER BY id;

-- name: ListNamingFormats :many
SELECT naming_profile_id, slot, template FROM naming_format ORDER BY naming_profile_id, slot;

-- name: CreateNamingProfile :one
INSERT INTO naming_profile (name, rename_files, replace_illegal_characters, colon_replacement)
VALUES (?, ?, ?, ?)
RETURNING id;

-- name: UpdateNamingProfile :exec
UPDATE naming_profile SET rename_files = ?, replace_illegal_characters = ?, colon_replacement = ?
WHERE id = ?;

-- name: DeleteNamingFormats :exec
DELETE FROM naming_format WHERE naming_profile_id = ?;

-- name: InsertNamingFormat :exec
INSERT INTO naming_format (naming_profile_id, slot, template) VALUES (?, ?, ?);

-- name: SetCategoryNamingProfile :exec
UPDATE category SET naming_profile_id = ? WHERE id = ?;

-- name: DeleteNamingProfile :exec
DELETE FROM naming_profile WHERE id = ?;
