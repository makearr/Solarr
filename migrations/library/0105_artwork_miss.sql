-- A cover source asked about a work that answered none: the artwork sweep skips that pair until the miss is old, instead of
-- asking again every half hour (680 albums, about seven minutes a run, measured on a live library).
CREATE TABLE artwork_miss (
  subject_type TEXT    NOT NULL,
  subject_id   INTEGER NOT NULL,
  source       TEXT    NOT NULL,
  missed_at    INTEGER NOT NULL,
  PRIMARY KEY (subject_type, subject_id, source)
) WITHOUT ROWID;
