-- Revision D3: identified files get their provider ids written into their tags, fill-only,
-- per library and on by default; read-only roots are never written whatever this says.
ALTER TABLE category_file_management ADD COLUMN write_ids_to_tags INTEGER NOT NULL DEFAULT 1
  CHECK (write_ids_to_tags IN (0, 1));
