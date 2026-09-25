-- The shipped "AAC 160/48kHz/16-bit (m4a)" profile was the user's own target written as a profile;
-- a library's quality target replaces it (2026-09-25). Kept wherever something already uses it.
DELETE FROM quality_profile
WHERE name = 'AAC 160/48kHz/16-bit (m4a)'
  AND id NOT IN (SELECT quality_profile_id FROM category WHERE quality_profile_id IS NOT NULL)
  AND id NOT IN (SELECT quality_cutoff_profile_id FROM category WHERE quality_cutoff_profile_id IS NOT NULL)
  AND id NOT IN (SELECT quality_profile_id FROM category_quality_profile)
  AND id NOT IN (SELECT quality_profile_id FROM import_list WHERE quality_profile_id IS NOT NULL);
