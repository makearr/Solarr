-- Denormalized cache of a work's primary artist credit (role='artist', ord=0),
-- kept in sync by internal/library.People.AddCredit -- the only writer of
-- credit rows, the same one-choke-point reasoning work.year/work_date already
-- establish. Exists so the Tracks navigation level (ADR-54) can be ordered
-- Artist -> Album -> Disc -> Track at real scale: the correlated per-row
-- subquery into credit/person internal/library/works.go already uses (and
-- deliberately keeps its own Artist column unsortable over, for exactly this
-- reason) has no supporting index and would sort the whole result at hundreds
-- of thousands of rows (ADR-28).
ALTER TABLE work ADD COLUMN primary_artist_id INTEGER REFERENCES person(id);
ALTER TABLE work ADD COLUMN primary_artist_sort_name TEXT;
CREATE INDEX work_primary_artist_sort ON work(primary_artist_sort_name, sort_title, id);
