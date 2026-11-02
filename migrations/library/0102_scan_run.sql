-- One sweep can span several job runs. run_started_at is when it began (Unix seconds, 0 for a row older than this
-- migration), which is what a finished sweep stores as its watermark; run_full says it was a full sweep, so a resumed one
-- still proves what it did not see is gone; files_seen counts the files committed so far, for its progress.
ALTER TABLE scan_state ADD COLUMN run_started_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scan_state ADD COLUMN run_full INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scan_state ADD COLUMN files_seen INTEGER NOT NULL DEFAULT 0;
