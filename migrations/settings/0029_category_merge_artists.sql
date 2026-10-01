-- A library's option to fold an artist stored under several names into one (M1g 1.2), after each disk scan.
ALTER TABLE category_file_management ADD COLUMN merge_artist_aliases INTEGER NOT NULL DEFAULT 0 CHECK (merge_artist_aliases IN (0, 1));
