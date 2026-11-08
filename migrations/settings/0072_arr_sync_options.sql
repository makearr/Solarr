-- What a Prowlarr connection keeps in step with Solarr: JSON, absent until the owner sets it.
ALTER TABLE arr_instance ADD COLUMN sync_json TEXT;
