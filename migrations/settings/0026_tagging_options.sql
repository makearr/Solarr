-- Per library, beside naming (2026-09-29): replace ids a file already carries, write the
-- providers' album, artist, title, numbering, date and genre, and embed a fetched cover.
ALTER TABLE category_file_management ADD COLUMN replace_ids_in_tags INTEGER NOT NULL DEFAULT 0
  CHECK (replace_ids_in_tags IN (0, 1));
ALTER TABLE category_file_management ADD COLUMN write_metadata_to_tags INTEGER NOT NULL DEFAULT 0
  CHECK (write_metadata_to_tags IN (0, 1));
ALTER TABLE category_file_management ADD COLUMN embed_cover_in_tags INTEGER NOT NULL DEFAULT 0
  CHECK (embed_cover_in_tags IN (0, 1));
