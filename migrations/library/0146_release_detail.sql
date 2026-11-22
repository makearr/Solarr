-- What tells editions and tracks apart: kept beside edition and work_part so their column lists stay as they are.
CREATE TABLE edition_detail (
  edition_id     INTEGER PRIMARY KEY REFERENCES edition(id) ON DELETE CASCADE,
  disambiguation TEXT NOT NULL DEFAULT '',
  packaging      TEXT NOT NULL DEFAULT '',
  language       TEXT NOT NULL DEFAULT '',
  script         TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;

CREATE TABLE work_part_detail (
  part_id       INTEGER PRIMARY KEY REFERENCES work_part(id) ON DELETE CASCADE,
  medium_title  TEXT NOT NULL DEFAULT '',
  -- The track's credited artists as one display string ("A feat. B"); empty means the album artist.
  artist_credit TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
