-- The language the lyrics of a music file are in, read by the probe from its tags (a JSON array, like languages).
ALTER TABLE media_attributes ADD COLUMN lyrics_languages TEXT;
