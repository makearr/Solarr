-- Lidarr's naming model (2026-09-19 review): templates per slot the media type declares, plus
-- its character options. One profile per library, created on first save.
CREATE TABLE naming_format (
  naming_profile_id INTEGER NOT NULL REFERENCES naming_profile(id) ON DELETE CASCADE,
  slot              TEXT    NOT NULL,
  template          TEXT    NOT NULL,
  PRIMARY KEY (naming_profile_id, slot)
) WITHOUT ROWID;

ALTER TABLE naming_profile ADD COLUMN rename_files INTEGER NOT NULL DEFAULT 1;
ALTER TABLE naming_profile ADD COLUMN replace_illegal_characters INTEGER NOT NULL DEFAULT 1;
ALTER TABLE naming_profile ADD COLUMN colon_replacement TEXT NOT NULL DEFAULT 'smart';

-- 0013's seed never had a reader; a library without a profile uses its media type's default.
DELETE FROM naming_profile
WHERE id NOT IN (SELECT naming_profile_id FROM category WHERE naming_profile_id IS NOT NULL);

ALTER TABLE naming_profile DROP COLUMN folder_template;
ALTER TABLE naming_profile DROP COLUMN file_template;
