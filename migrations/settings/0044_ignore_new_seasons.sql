-- Sonarr's "Monitor New Seasons: None" per TV library; off keeps the old behaviour of following the series.
ALTER TABLE category_file_management ADD COLUMN ignore_new_seasons INTEGER NOT NULL DEFAULT 0 CHECK (ignore_new_seasons IN (0, 1));
