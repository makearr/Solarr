-- One broadcast under two series (Stage 8 T23): across filesystems, the second series gets a copy or a symlink.
ALTER TABLE category_file_management ADD COLUMN shared_file_fallback TEXT NOT NULL DEFAULT 'copy' CHECK (shared_file_fallback IN ('copy', 'symlink'));
