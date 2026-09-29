-- Who asked for a held release (2026-09-29): a user, an RSS sync or an automatic search. The queue
-- shows it, and the order it sends in follows it: user, then RSS, then new, then backfill.
ALTER TABLE ready_queue ADD COLUMN origin TEXT NOT NULL DEFAULT 'automatic'
  CHECK (origin IN ('user', 'rss', 'automatic'));
