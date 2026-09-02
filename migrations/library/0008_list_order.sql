-- The unfiltered library list, which is the first thing anyone sees.
-- Pure ASCII, or sqlc emits wrong SQL that still compiles.

-- work_type_sort leads on media_type, so it cannot order a list that does not filter
-- on one: SQLite scans the table and sorts all of it for every page. work is a rowid
-- table and id is its rowid, so an index on sort_title alone is already (sort_title,
-- id) -- exactly the order the cursor pages through (documentation/benchmarks.md).
CREATE INDEX work_sort ON work(sort_title);
