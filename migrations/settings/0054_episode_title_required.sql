-- Sonarr's Episode Title Required: an episode whose title is still TBA is held from import for up to 48 hours after
-- it aired, always, only from a whole-season release, or never. Sonarr's default is always.
ALTER TABLE category_file_management ADD COLUMN episode_title_required TEXT NOT NULL DEFAULT 'always'
  CHECK (episode_title_required IN ('always', 'bulk_season', 'never'));
