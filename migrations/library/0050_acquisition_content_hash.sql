-- What was sent, by content (2026-09-29): for usenet a hash of the NZB's article ids, the same
-- wherever that upload is listed. A failure blocklists it, so another indexer's copy is refused.
ALTER TABLE acquisition ADD COLUMN content_hash TEXT;
