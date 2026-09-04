-- section 19.6, todo 4.4: what a scan should do with an album it finds on disk
-- that nobody asked for. Per-category, not global, so the live library
-- (adopted, everything already wanted) and the test library (built up from
-- nothing) can carry different policy.

ALTER TABLE category ADD COLUMN monitor_discovered TEXT NOT NULL DEFAULT 'monitored'
  CHECK (monitor_discovered IN ('monitored', 'unmonitored', 'none'));
