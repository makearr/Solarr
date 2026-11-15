-- The file that took a recycled file's place, so reverting its import can bring the old one back. No foreign key:
-- the entry outlives the media_object rows.
ALTER TABLE recycle_bin ADD COLUMN replaced_by_media_object_id INTEGER;
CREATE INDEX recycle_bin_by_replacement ON recycle_bin(replaced_by_media_object_id) WHERE replaced_by_media_object_id IS NOT NULL AND restored_at IS NULL;
