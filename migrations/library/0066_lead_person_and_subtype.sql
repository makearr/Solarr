-- Core columns lose their music names (revision 2, D2): a work is listed under its type's lead person, an album's
-- artist or a movie's director, and its subtype is the type's own (an album's release type).
ALTER TABLE work RENAME COLUMN primary_artist_id TO lead_person_id;
ALTER TABLE work RENAME COLUMN primary_artist_sort_name TO lead_person_sort_name;
ALTER TABLE work RENAME COLUMN primary_type TO subtype;
DROP INDEX work_primary_artist_sort;
CREATE INDEX work_lead_person_sort
  ON work(COALESCE(lead_person_sort_name, '') COLLATE NOCASE, sort_title, id);
