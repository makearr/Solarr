-- The calendar asks for every release in a date window, which had no index to
-- answer it: work_date's primary key is (work_id, kind, region), so a query
-- filtering on kind and date scanned the whole table. ADR-28's freshness count
-- (CountWorksFreshSince) has the same shape and gains from this too.
CREATE INDEX work_date_kind_date ON work_date(kind, date);
