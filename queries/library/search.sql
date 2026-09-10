-- Ranked cross-media search over titles and aliases (migration 0010, section 21.1).

-- name: SearchWorks :many
-- One row per matching work, even if several of its titles match: the best (lowest) bm25
-- rank wins, so a common alias never crowds out the rest of the results. media_type is
-- filtered on title_search directly, before the join, since MATCH has already cut the
-- candidate set down from the full corpus.
--
-- ADR-27 category visibility (migration 0012's work_root_folder bridge, same as
-- addVisibility in internal/db/library/works.go) is applied as a static predicate here
-- rather than composed in Go: MATCH has already cut ranked down to a handful of rows, so
-- an index on that filter does not matter the way it does for the unfiltered work-table
-- scan composeList guards against. filter_root_folders=0 and filter_categories=0 together
-- mean fully unfiltered; each flag that is set matches its own subquery, OR'd, so a
-- subscription-discovered work (no root folder, but a desired_item.category_id) is found
-- the same way as a scanned one -- either side empty means the caller can see nothing
-- via that path, matching zero rows rather than being indistinguishable from unfiltered
-- (json_each of jobs.sql's visible_categories is the precedent for passing a Go slice as
-- a JSON array).
WITH ranked AS (
  SELECT ts.work_id AS work_id, CAST(MIN(ts.rank) AS REAL) AS score
  FROM title_search ts
  WHERE ts.title MATCH sqlc.arg(query)
    AND (CAST(sqlc.arg(media_type) AS TEXT) = '' OR ts.media_type = sqlc.arg(media_type))
  GROUP BY ts.work_id
)
SELECT w.id, w.media_type, w.canonical_title, w.sort_title, w.original_language,
       w.year, w.runtime_ms, w.added_at, w.updated_at, w.added_by, r.score
FROM ranked r
JOIN work w ON w.id = r.work_id
WHERE (sqlc.arg(filter_root_folders) = 0 AND sqlc.arg(filter_categories) = 0)
   OR (sqlc.arg(filter_root_folders) = 1 AND r.work_id IN (
        SELECT work_id FROM work_root_folder
        WHERE root_folder_id IN (SELECT value FROM json_each(sqlc.arg(root_folder_ids)))
      ))
   OR (sqlc.arg(filter_categories) = 1 AND r.work_id IN (
        SELECT work_id FROM desired_item
        WHERE category_id IN (SELECT value FROM json_each(sqlc.arg(category_ids)))
      ))
ORDER BY r.score
LIMIT sqlc.arg(page_size);
