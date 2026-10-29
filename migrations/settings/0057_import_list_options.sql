-- An import list's own settings by field key as JSON (documentation/import-lists.md), and the media type's monitor
-- option its works are added with; empty is the type's default.
ALTER TABLE import_list ADD COLUMN options TEXT NOT NULL DEFAULT '';
ALTER TABLE import_list ADD COLUMN monitor_option TEXT NOT NULL DEFAULT '';
-- Nothing ever synced against a list before, so none has been previewed; each is enabled again after its preview.
UPDATE import_list SET enabled = 0;
