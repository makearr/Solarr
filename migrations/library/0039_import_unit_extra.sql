-- Extra files an import brought along (Lidarr's "Import Extra Files", 2026-09-24). They are not
-- media, so revert finds them here rather than through media_object. root_folder_id is a
-- settings.db id: no FK across database files.
CREATE TABLE import_unit_extra (
  id             INTEGER PRIMARY KEY,
  unit_id        INTEGER NOT NULL REFERENCES import_unit(id) ON DELETE CASCADE,
  root_folder_id INTEGER NOT NULL,
  rel_path       TEXT    NOT NULL
);
CREATE INDEX idx_import_unit_extra_unit ON import_unit_extra(unit_id);
