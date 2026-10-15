-- Jellyfin/Kodi files (Stage 8 T24): nfo files and artwork beside a library's own, written only where none exists.
ALTER TABLE category_file_management ADD COLUMN media_server_files INTEGER NOT NULL DEFAULT 0 CHECK (media_server_files IN (0, 1));
