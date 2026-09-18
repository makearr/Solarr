-- Candidates, decisions and the provenance chain (section 5.4) (ADR-25, section 4.3).

CREATE TABLE candidate (
  id               INTEGER PRIMARY KEY,
  indexer_id       INTEGER NOT NULL,
  guid             TEXT    NOT NULL,
  title            TEXT    NOT NULL,
  -- Claimed only: there is nothing to probe before downloading, so the bitrate
  -- fields are null and the rate is estimated from size over runtime (ADR-39).
  attributes_id    INTEGER REFERENCES media_attributes(id),
  size_bytes       INTEGER,
  seeders          INTEGER,
  leechers         INTEGER,
  published_at     INTEGER,
  protocol         TEXT    NOT NULL
                   CHECK (protocol IN ('usenet', 'torrent', 'ddl', 'feed')),
  -- For ddl and feed this is the landing page or entry, not the file itself.
  download_url     TEXT    NOT NULL,
  parse_confidence REAL,
  source           TEXT    NOT NULL CHECK (source IN ('rss', 'search')),
  first_seen_at    INTEGER NOT NULL,
  expires_at       INTEGER NOT NULL,
  UNIQUE(indexer_id, guid)
);
CREATE INDEX candidate_expiry ON candidate(expires_at);

-- The "why?" record (F1), here rather than history.db so the query is one join.
CREATE TABLE candidate_decision (
  id               INTEGER PRIMARY KEY,
  candidate_id     INTEGER NOT NULL REFERENCES candidate(id) ON DELETE CASCADE,
  desired_item_id  INTEGER NOT NULL REFERENCES desired_item(id) ON DELETE CASCADE,
  outcome          TEXT    NOT NULL CHECK (outcome IN ('ACCEPT', 'REJECT', 'DEFER')),
  score            REAL    NOT NULL,
  components_json  TEXT    NOT NULL,
  rejections_json  TEXT,
  deferrals_json   TEXT,
  match_confidence REAL,
  decided_at       INTEGER NOT NULL
);
CREATE INDEX decision_by_item ON candidate_decision(desired_item_id, decided_at DESC);

-- Denormalised, because candidates expire and the provenance must not (section 12).
CREATE TABLE acquisition (
  id                  INTEGER PRIMARY KEY,
  desired_item_id     INTEGER REFERENCES desired_item(id),
  candidate_id        INTEGER REFERENCES candidate(id) ON DELETE SET NULL,
  indexer_id          INTEGER,
  candidate_guid      TEXT,
  candidate_title     TEXT,
  search_terms        TEXT,
  downloader_id       INTEGER NOT NULL,
  download_client_ref TEXT    NOT NULL,
  download_dir        TEXT,
  client_category     TEXT,
  -- A reference whose fingerprint no longer matches is a different download, so
  -- the old acquisition closes rather than adopting its files (section 10.9).
  client_fingerprint  TEXT,
  protocol            TEXT    NOT NULL
                      CHECK (protocol IN ('usenet', 'torrent', 'ddl', 'feed')),
  -- ddl and feed only: Solarr is the client and does not survive a restart
  -- the way SABnzbd does, so progress is persisted and resumed by range (ADR-46).
  ddl_resolved_url    TEXT,
  ddl_bytes_done      INTEGER NOT NULL DEFAULT 0,
  ddl_resumable       INTEGER,
  state               TEXT    NOT NULL,
  last_error          TEXT,
  created_at          INTEGER NOT NULL,
  updated_at          INTEGER NOT NULL,
  UNIQUE(downloader_id, download_client_ref)
);
-- At most one in-flight acquisition per desired item (Invariant 15).
CREATE UNIQUE INDEX acquisition_one_inflight ON acquisition(desired_item_id)
  WHERE state NOT IN ('IMPORTED', 'CLEANED', 'FAILED', 'CANCELLED');
CREATE INDEX acquisition_active ON acquisition(state)
  WHERE state NOT IN ('IMPORTED', 'CLEANED', 'FAILED', 'CANCELLED');

-- Keyed two ways because neither alone is enough: the guid identifies a listing
-- on one indexer, the content hash identifies the payload wherever it appears.
CREATE TABLE blocklisted_release (
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
                                    'IMPORT_FAILED', 'REJECTED_WORSE', 'MANUAL')),
  detail_json     TEXT,
  source          TEXT    NOT NULL CHECK (source IN ('auto', 'user')),
  -- Null is permanent; auto entries expire because indexers repair things.
  expires_at      INTEGER,
  created_at      INTEGER NOT NULL,
  CHECK (guid IS NOT NULL OR content_hash IS NOT NULL)
);
CREATE UNIQUE INDEX blocklist_by_guid ON blocklisted_release(indexer_id, guid)
  WHERE guid IS NOT NULL;
CREATE INDEX blocklist_by_hash ON blocklisted_release(content_hash)
  WHERE content_hash IS NOT NULL;

-- Retained so an automatic choice stays visible and overridable (ADR-40).
CREATE TABLE import_decision (
  id                  INTEGER PRIMARY KEY,
  desired_item_id     INTEGER NOT NULL REFERENCES desired_item(id) ON DELETE CASCADE,
  acquisition_id      INTEGER REFERENCES acquisition(id) ON DELETE SET NULL,
  incumbent_object_id INTEGER REFERENCES media_object(id) ON DELETE SET NULL,
  arrived_probe_json  TEXT    NOT NULL,
  comparison_json     TEXT    NOT NULL,
  outcome             TEXT    NOT NULL
                      CHECK (outcome IN ('IMPORT', 'REJECT_WORSE',
                                         'REJECT_DUPLICATE', 'NEEDS_REVIEW')),
  reason_json         TEXT    NOT NULL,
  decided_at          INTEGER NOT NULL,
  overridden_by       INTEGER,
  overridden_at       INTEGER
);
CREATE INDEX import_decision_pending ON import_decision(decided_at DESC)
  WHERE outcome = 'NEEDS_REVIEW';

-- Nothing is deleted directly (section 10.8): a same-filesystem move, bounded by
-- time and space, restorable with its provenance intact.
CREATE TABLE quarantine (
  id              INTEGER PRIMARY KEY,
  original_path   TEXT    NOT NULL,
  quarantine_path TEXT    NOT NULL UNIQUE,
  root_folder_id  INTEGER,
  media_object_id INTEGER REFERENCES media_object(id) ON DELETE SET NULL,
  acquisition_id  INTEGER REFERENCES acquisition(id) ON DELETE SET NULL,
  reason          TEXT    NOT NULL,
  -- Counts against the free-space gate, so quarantine cannot cause the condition
  -- it exists to prevent (ADR-12).
  size_bytes      INTEGER NOT NULL,
  quarantined_at  INTEGER NOT NULL,
  expires_at      INTEGER NOT NULL,
  restored_at     INTEGER
);
CREATE INDEX quarantine_expiry ON quarantine(expires_at) WHERE restored_at IS NULL;
CREATE INDEX quarantine_by_root ON quarantine(root_folder_id) WHERE restored_at IS NULL;

CREATE TABLE acquisition_file (
  id              INTEGER PRIMARY KEY,
  acquisition_id  INTEGER NOT NULL REFERENCES acquisition(id) ON DELETE CASCADE,
  source_path     TEXT    NOT NULL,
  media_object_id INTEGER REFERENCES media_object(id),
  library_path_id INTEGER REFERENCES library_path(id),
  imported_at     INTEGER
);
CREATE INDEX acq_file_object ON acquisition_file(media_object_id);
