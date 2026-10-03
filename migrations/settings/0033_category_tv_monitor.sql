-- What a scan does with a series it finds: monitor the episodes on disk, every episode of the series, or none.
ALTER TABLE category ADD COLUMN tv_monitor TEXT NOT NULL DEFAULT 'existing' CHECK (tv_monitor IN ('existing', 'all', 'none'));
