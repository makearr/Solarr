-- A connection may send the monitored flag of what it imported back to its *Arr (off unless its owner turns it on).
ALTER TABLE arr_instance ADD COLUMN push_monitoring INTEGER NOT NULL DEFAULT 0 CHECK (push_monitoring IN (0, 1));
ALTER TABLE arr_instance ADD COLUMN last_pushed_at INTEGER;
