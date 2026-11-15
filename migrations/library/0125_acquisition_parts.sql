-- The parts (media refs) a download was judged to cover; [] is the whole item. Several downloads of one item may be in
-- flight when their parts do not overlap, so the one-in-flight unique index becomes a plain one and the code checks.
ALTER TABLE acquisition ADD COLUMN parts_json TEXT NOT NULL DEFAULT '[]';
DROP INDEX acquisition_one_inflight;
CREATE INDEX acquisition_inflight_by_item ON acquisition(desired_item_id)
  WHERE state NOT IN ('IMPORTED', 'CLEANED', 'FAILED', 'CANCELLED');
