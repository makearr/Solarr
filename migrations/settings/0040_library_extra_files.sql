-- Import Extra Files is each library's own (user, 2026-10-14). NULL takes the media type's default (TV on, for
-- subtitles); a stored library keeps the old instance-wide choice, except TV, which starts on its default.
ALTER TABLE category_file_management ADD COLUMN import_extra_files INTEGER CHECK (import_extra_files IN (0, 1));
ALTER TABLE category_file_management ADD COLUMN extra_file_extensions TEXT;
UPDATE category_file_management SET
  import_extra_files = (SELECT CASE WHEN s.value_json = 'true' THEN 1 ELSE 0 END
                        FROM instance_setting s WHERE s.key = 'import.extra_files'),
  extra_file_extensions = (SELECT s.value_json FROM instance_setting s WHERE s.key = 'import.extra_file_extensions')
WHERE (SELECT c.media_type FROM category c WHERE c.id = category_file_management.category_id) <> 'tv';
DELETE FROM instance_setting WHERE key IN ('import.extra_files', 'import.extra_file_extensions');
