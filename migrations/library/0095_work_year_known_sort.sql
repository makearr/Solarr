-- The year sort orders and pages on COALESCE(year, 0): a cursor compared against a NULL year matches nothing, so the
-- works with no known year fell out of every page after the first. Must match library.yearSortExpression exactly.
CREATE INDEX work_year_known_sort ON work(COALESCE(year, 0), id);
