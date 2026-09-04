-- Cursor-paginated sort by year or added_at (section 21.2's configurable
-- columns) needs its own index, same reasoning as work_sort for sort_title:
-- an unindexed ORDER BY sorts the whole table (documentation/sql-and-sqlc.md).
CREATE INDEX work_year_sort ON work(year, id);
CREATE INDEX work_added_sort ON work(added_at, id);
