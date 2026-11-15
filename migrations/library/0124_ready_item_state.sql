-- The item's state when a release was held; empty on older rows means do not compare.
ALTER TABLE ready_queue ADD COLUMN item_state TEXT NOT NULL DEFAULT '';
