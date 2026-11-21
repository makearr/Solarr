-- migrate:disable-foreign-keys
-- A release its indexer stopped serving gets its own reason, GONE_AT_INDEXER, instead of passing as a failed download.
CREATE TABLE "blocklisted_release_new" (
  id              INTEGER PRIMARY KEY,
  -- Null when blocked by content hash alone.
  indexer_id      INTEGER,
  guid            TEXT,
  content_hash    TEXT,
  -- Kept for the interface; guids are unreadable.
  title           TEXT    NOT NULL,
  desired_item_id INTEGER REFERENCES desired_item(id) ON DELETE SET NULL,
  reason          TEXT    NOT NULL
                  CHECK (reason IN ('DOWNLOAD_FAILED', 'STALLED', 'CORRUPT',
                                    'IMPORT_FAILED', 'REJECTED_WORSE', 'MANUAL', 'GONE_AT_INDEXER')),
  detail_json     TEXT,
  source          TEXT    NOT NULL CHECK (source IN ('auto', 'user')),
  -- Null is permanent; auto entries expire because indexers repair things.
  expires_at      INTEGER,
  created_at      INTEGER NOT NULL,
  CHECK (guid IS NOT NULL OR content_hash IS NOT NULL)
);

INSERT INTO blocklisted_release_new (id, indexer_id, guid, content_hash, title, desired_item_id, reason, detail_json, source, expires_at, created_at)
SELECT id, indexer_id, guid, content_hash, title, desired_item_id, reason, detail_json, source, expires_at, created_at
FROM blocklisted_release;

-- What the unavailable memory wrote before this reason existed.
UPDATE blocklisted_release_new SET reason = 'GONE_AT_INDEXER'
WHERE reason = 'DOWNLOAD_FAILED' AND source = 'auto' AND detail_json = '{"cause":"unavailable"}';

DROP TABLE blocklisted_release;
ALTER TABLE blocklisted_release_new RENAME TO blocklisted_release;

CREATE UNIQUE INDEX blocklist_by_guid ON blocklisted_release(indexer_id, guid)
  WHERE guid IS NOT NULL;
CREATE INDEX blocklist_by_hash ON blocklisted_release(content_hash)
  WHERE content_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS blocklisted_release_by_desired_item ON blocklisted_release(desired_item_id) WHERE desired_item_id IS NOT NULL;
