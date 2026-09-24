-- Download folders are watched for imports (2026-09-19 review): each settled entry is imported
-- once. transfer_ladder is the folder's own order, NULL for the Import screen's.
ALTER TABLE download_folder ADD COLUMN watch INTEGER NOT NULL DEFAULT 1 CHECK (watch IN (0, 1));
ALTER TABLE download_folder ADD COLUMN transfer_ladder TEXT;
