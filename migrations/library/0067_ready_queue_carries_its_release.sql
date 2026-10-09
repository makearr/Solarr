-- A held release carries what sending it needs, so dispatch never reads the candidate or its decision: they move to
-- statistics.db, which nothing that decides may read (revision 2, D7). candidate_id stays as a plain reference.
CREATE TABLE ready_queue_release (
  candidate_id    INTEGER PRIMARY KEY,
  desired_item_id INTEGER NOT NULL REFERENCES desired_item(id) ON DELETE CASCADE,
  priority        INTEGER NOT NULL,
  queued_at       INTEGER NOT NULL,
  expires_at      INTEGER NOT NULL,
  size_bytes      INTEGER,
  origin          TEXT    NOT NULL DEFAULT 'automatic',
  indexer_id      INTEGER NOT NULL,
  guid            TEXT    NOT NULL,
  title           TEXT    NOT NULL,
  download_url    TEXT    NOT NULL,
  protocol        TEXT    NOT NULL,
  score           REAL
);
INSERT INTO ready_queue_release (candidate_id, desired_item_id, priority, queued_at, expires_at, size_bytes, origin,
                                 indexer_id, guid, title, download_url, protocol, score)
SELECT r.candidate_id, r.desired_item_id, r.priority, r.queued_at, r.expires_at, COALESCE(r.size_bytes, c.size_bytes), r.origin,
       c.indexer_id, c.guid, c.title, c.download_url, c.protocol,
       (SELECT MAX(d.score) FROM candidate_decision d WHERE d.candidate_id = r.candidate_id AND d.desired_item_id = r.desired_item_id)
FROM ready_queue r JOIN candidate c ON c.id = r.candidate_id;
DROP TABLE ready_queue;
ALTER TABLE ready_queue_release RENAME TO ready_queue;
CREATE INDEX ready_queue_order ON ready_queue(priority DESC, queued_at ASC);
