-- Every live path of every media object that lives at more than one place; roots are filtered in Go.
-- name: ListSharedObjectPaths :many
SELECT lp.id, lp.media_object_id, lp.root_folder_id, lp.rel_path, lp.link_kind, o.size_bytes
FROM library_path lp
JOIN media_object o ON o.id = lp.media_object_id
WHERE lp.missing_since IS NULL
  AND lp.media_object_id IN (
    SELECT media_object_id FROM library_path WHERE missing_since IS NULL
    GROUP BY media_object_id HAVING count(*) > 1)
ORDER BY lp.media_object_id, lp.is_primary DESC, lp.id;

-- name: SetLibraryPathLinkKind :exec
UPDATE library_path SET link_kind = ? WHERE id = ?;
