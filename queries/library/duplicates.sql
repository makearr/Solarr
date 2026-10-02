-- Files with the same size and the same sampled content, whatever their names: the likely copies of one file.
-- name: ListDuplicateGroups :many
SELECT quick_hash, size_bytes, CAST(COUNT(*) AS INTEGER) AS copies
FROM media_object
WHERE quick_hash <> ''
GROUP BY quick_hash, size_bytes
HAVING COUNT(*) > 1
ORDER BY size_bytes * (COUNT(*) - 1) DESC, quick_hash
LIMIT sqlc.arg(page_size);

-- name: ListDuplicateMembers :many
SELECT mo.id AS media_object_id, lp.root_folder_id, lp.rel_path
FROM media_object mo
JOIN library_path lp ON lp.media_object_id = mo.id
WHERE mo.quick_hash = sqlc.arg(quick_hash) AND mo.size_bytes = sqlc.arg(size_bytes)
ORDER BY mo.id, lp.is_primary DESC, lp.id;
