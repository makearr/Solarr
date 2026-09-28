-- Artist order ignores case, as Lidarr's does: "bumble" sorts beside "Bush", not after "Ahei".
-- work.sort_title is stored lower-cased already; sort names keep the provider's case.
DROP INDEX person_sort;
CREATE INDEX person_sort ON person(sort_name COLLATE NOCASE);
DROP INDEX organisation_sort;
CREATE INDEX organisation_sort ON organisation(sort_name COLLATE NOCASE);
DROP INDEX work_primary_artist_sort;
CREATE INDEX work_primary_artist_sort
  ON work(COALESCE(primary_artist_sort_name, '') COLLATE NOCASE, sort_title, id);
