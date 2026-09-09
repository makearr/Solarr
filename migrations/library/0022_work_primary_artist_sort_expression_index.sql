-- 0021's index was built on the raw primary_artist_sort_name column, but every
-- real query needing it (internal/library/trackparts.go's composeAlbumPage)
-- orders by COALESCE(primary_artist_sort_name, '') -- so a work with no
-- primary artist credit yet sorts predictably instead of leaning on however
-- SQLite happens to place NULL in that particular expression context. An
-- index only serves a query whose WHERE/ORDER BY expression matches it
-- exactly, so the raw-column index left the query needing "USE TEMP B-TREE
-- FOR ORDER BY" regardless -- confirmed by EXPLAIN QUERY PLAN, not assumed.
DROP INDEX work_primary_artist_sort;
CREATE INDEX work_primary_artist_sort
  ON work(COALESCE(primary_artist_sort_name, ''), sort_title, id);
