-- The media files a unit held when it was inspected, kept for units that share a folder: a held unit is found again by
-- what it is, not by its position in a new split of the folder. Paths are relative to the unit's own folder.
CREATE TABLE import_unit_media (
  unit_id    INTEGER NOT NULL REFERENCES import_unit(id) ON DELETE CASCADE,
  rel_path   TEXT    NOT NULL,
  size_bytes INTEGER NOT NULL,
  PRIMARY KEY (unit_id, rel_path)
) WITHOUT ROWID;
