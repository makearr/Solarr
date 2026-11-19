-- When a provider was last asked for the collection's picture and count, so an unanswerable one is not asked on every page view.
ALTER TABLE collection ADD COLUMN facts_checked_at INTEGER NOT NULL DEFAULT 0;
