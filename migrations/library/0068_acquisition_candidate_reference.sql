-- migrate:disable-foreign-keys
--
-- An acquisition's candidate_id becomes a plain reference: candidates move to statistics.db (revision 2, D7), and an
-- acquisition already copies the guid and title it needs; it now keeps the size too, which the free-space gate sums. A rebuild, since SQLite cannot drop a foreign key; three
-- tables reference acquisition(id), so foreign keys are off for this migration only and every id is kept.
CREATE TABLE acquisition_new (
  id                  INTEGER PRIMARY KEY,
  desired_item_id     INTEGER REFERENCES desired_item(id),
  candidate_id        INTEGER,
  indexer_id          INTEGER,
  candidate_guid      TEXT,
  candidate_title     TEXT,
  search_terms        TEXT,
  downloader_id       INTEGER NOT NULL,
  download_client_ref TEXT    NOT NULL,
  download_dir        TEXT,
  client_category     TEXT,
  client_fingerprint  TEXT,
  protocol            TEXT    NOT NULL
                      CHECK (protocol IN ('usenet', 'torrent', 'ddl', 'feed')),
  ddl_resolved_url    TEXT,
  ddl_bytes_done      INTEGER NOT NULL DEFAULT 0,
  ddl_resumable       INTEGER,
  state               TEXT    NOT NULL,
  last_error          TEXT,
  created_at          INTEGER NOT NULL,
  updated_at          INTEGER NOT NULL,
  content_hash        TEXT,
  size_bytes          INTEGER,
  UNIQUE(downloader_id, download_client_ref)
);
INSERT INTO acquisition_new (id, desired_item_id, candidate_id, indexer_id, candidate_guid, candidate_title, search_terms,
                             downloader_id, download_client_ref, download_dir, client_category, client_fingerprint,
                             protocol, ddl_resolved_url, ddl_bytes_done, ddl_resumable, state, last_error, created_at,
                             updated_at, content_hash, size_bytes)
SELECT id, desired_item_id, candidate_id, indexer_id, candidate_guid, candidate_title, search_terms,
       downloader_id, download_client_ref, download_dir, client_category, client_fingerprint,
       protocol, ddl_resolved_url, ddl_bytes_done, ddl_resumable, state, last_error, created_at,
       updated_at, content_hash, (SELECT c.size_bytes FROM candidate c WHERE c.id = acquisition.candidate_id)
FROM acquisition;
DROP TABLE acquisition;
ALTER TABLE acquisition_new RENAME TO acquisition;
CREATE UNIQUE INDEX acquisition_one_inflight ON acquisition(desired_item_id)
  WHERE state NOT IN ('IMPORTED', 'CLEANED', 'FAILED', 'CANCELLED');
CREATE INDEX acquisition_active ON acquisition(state)
  WHERE state NOT IN ('IMPORTED', 'CLEANED', 'FAILED', 'CANCELLED');
