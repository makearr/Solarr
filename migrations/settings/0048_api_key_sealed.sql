-- A sealed copy of the key so it can be shown again (user, 2026-10-23); NULL for keys made before.
ALTER TABLE api_key ADD COLUMN key_sealed BLOB;
