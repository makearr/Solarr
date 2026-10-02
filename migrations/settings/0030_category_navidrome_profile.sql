-- Navidrome's consumer profile (D10): the track-artist tags and ids it reads, and a cover.jpg beside the tracks.
ALTER TABLE category_file_management ADD COLUMN navidrome_profile INTEGER NOT NULL DEFAULT 0 CHECK (navidrome_profile IN (0, 1));
