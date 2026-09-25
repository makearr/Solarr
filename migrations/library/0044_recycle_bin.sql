-- "Quarantine" is called the recycle bin everywhere since 2026-09-25, the word the *Arrs use.
ALTER TABLE quarantine RENAME TO recycle_bin;
ALTER TABLE recycle_bin RENAME COLUMN quarantine_path TO recycled_path;
ALTER TABLE recycle_bin RENAME COLUMN quarantined_at TO recycled_at;
DROP INDEX quarantine_expiry;
DROP INDEX quarantine_by_root;
CREATE INDEX recycle_bin_expiry ON recycle_bin(expires_at) WHERE restored_at IS NULL;
CREATE INDEX recycle_bin_by_root ON recycle_bin(root_folder_id) WHERE restored_at IS NULL;
