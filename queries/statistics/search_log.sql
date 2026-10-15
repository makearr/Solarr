-- name: InsertSearchLog :exec
INSERT INTO search_log (at, category_id, desired_item_id, found) VALUES (?, ?, ?, ?);

-- Searches and finds per library since a time.
-- name: AggregateSearchesByCategory :many
SELECT category_id, CAST(COUNT(*) AS INTEGER) AS searches, CAST(COALESCE(SUM(found), 0) AS INTEGER) AS found
FROM search_log
WHERE at >= sqlc.arg(since)
GROUP BY category_id
ORDER BY category_id;

-- The oldest page of searches before a time, removed.
-- name: DeleteSearchLogBefore :execrows
DELETE FROM search_log WHERE id IN (
  SELECT old.id FROM search_log old WHERE old.at < sqlc.arg(before) ORDER BY old.at LIMIT sqlc.arg(page_size)
);
