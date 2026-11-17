-- Nothing reads the raw probe output (the decoded facts are in media_attributes and the media_object columns), and it was
-- a seventh of the file. The files shrink at the next VACUUM.
ALTER TABLE media_probe DROP COLUMN json;
ALTER TABLE media_object DROP COLUMN probe_json;
