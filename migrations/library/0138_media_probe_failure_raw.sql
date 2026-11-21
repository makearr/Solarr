-- What ffprobe printed for a file it could not read, cut to 4 KiB, for the person looking at the failure.
ALTER TABLE media_probe_failure ADD COLUMN raw TEXT NOT NULL DEFAULT '';
