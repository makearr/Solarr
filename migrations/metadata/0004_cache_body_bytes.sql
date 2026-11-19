-- The cache budget sums the bytes of every body; with the size stored beside it that is an index scan, not a read of the table.
ALTER TABLE metadata_cache ADD COLUMN body_bytes INTEGER NOT NULL DEFAULT 0;
UPDATE metadata_cache SET body_bytes = LENGTH(CAST(body AS BLOB));
DROP INDEX metadata_cache_by_age;
CREATE INDEX metadata_cache_by_age ON metadata_cache(fetched_at, body_bytes);
