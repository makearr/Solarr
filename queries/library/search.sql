-- Ranked cross-media search over titles and aliases (migration 0010, section 21.1).

-- name: SearchWorks :many
-- One row per matching work, even if several of its titles match: the best (lowest) bm25
-- rank wins, so a common alias never crowds out the rest of the results. media_type is
-- filtered on title_search directly, before the join, since MATCH has already cut the
-- candidate set down from the full corpus.
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
ORDER BY r.score
LIMIT sqlc.arg(page_size);
