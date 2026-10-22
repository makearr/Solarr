-- Every migration up to 0088, squashed into the schema they built (documentation/database-layout.md).
-- migrate:baseline replaces=ec0ab6ca4bb391475d988b31ec7388847ca862ae291b16ba34f58bf838208222

CREATE TABLE work (
  id                INTEGER PRIMARY KEY,
  media_type        TEXT    NOT NULL,
  canonical_title   TEXT    NOT NULL,
  sort_title        TEXT    NOT NULL,
  original_language TEXT,
  year              INTEGER,
  -- Expected runtime, for the pre-download bitrate estimate size/runtime (ADR-40).
  runtime_ms        INTEGER,
  added_at          INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL,
  -- No foreign key: app_user lives in settings.db (ADR-25, section 4.3).
  added_by          INTEGER
, description TEXT, lead_person_id INTEGER REFERENCES person(id), lead_person_sort_name TEXT, subtype TEXT);
CREATE INDEX work_type_sort ON work(media_type, sort_title);
CREATE INDEX work_sort ON work(sort_title);
CREATE INDEX work_year_sort ON work(year, id);
CREATE INDEX work_added_sort ON work(added_at, id);
CREATE INDEX work_lead_person_sort
  ON work(COALESCE(NULLIF(lead_person_sort_name, ''), char(1114111)) COLLATE NOCASE, sort_title, id);

CREATE TABLE work_date (
  work_id INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  kind    TEXT    NOT NULL,
  -- ISO country; '' is worldwide.
  region  TEXT    NOT NULL DEFAULT '',
  -- Partial dates allowed: 2024, 2024-03, 2024-03-01.
  date    TEXT    NOT NULL,
  source  TEXT    NOT NULL,
  PRIMARY KEY (work_id, kind, region)
) WITHOUT ROWID;
CREATE INDEX work_date_kind_date ON work_date(kind, date);

CREATE TABLE work_rating (
  work_id    INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  provider   TEXT    NOT NULL,
  -- Normalised 0..10.
  score      REAL    NOT NULL,
  votes      INTEGER,
  fetched_at INTEGER NOT NULL,
  PRIMARY KEY (work_id, provider)
) WITHOUT ROWID;

CREATE TABLE work_certification (
  work_id INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  system  TEXT    NOT NULL,
  rating  TEXT    NOT NULL,
  PRIMARY KEY (work_id, system)
) WITHOUT ROWID;

CREATE TABLE work_external_id (
  provider      TEXT    NOT NULL,
  external_id   TEXT    NOT NULL,
  work_id       INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  confidence    REAL    NOT NULL DEFAULT 1.0,
  first_seen_at INTEGER NOT NULL,
  PRIMARY KEY (provider, external_id)
) WITHOUT ROWID;
CREATE INDEX work_external_by_work ON work_external_id(work_id);

CREATE TABLE work_title (
  id         INTEGER PRIMARY KEY,
  work_id    INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  title      TEXT    NOT NULL,
  -- Normalised per section 10.2.
  normalized TEXT    NOT NULL,
  language   TEXT,
  kind       TEXT    NOT NULL,
  source     TEXT
);
CREATE INDEX work_title_norm ON work_title(normalized);
CREATE INDEX work_title_work ON work_title(work_id);

CREATE TABLE work_part (
  id         INTEGER PRIMARY KEY,
  work_id    INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  -- 's03e10' or 'disc1:track5'.
  media_ref  TEXT    NOT NULL,
  -- Module-supplied and padded, so ordering never parses the reference.
  sort_key   TEXT    NOT NULL DEFAULT '',
  title      TEXT,
  runtime_ms INTEGER,
  -- Partial dates as work_date: an episode air date outlives any intent to hold it.
  date       TEXT,
  -- Per-part monitoring inside a SET: the season, but not s03e05 (Appendix E, D3).
  monitored  INTEGER NOT NULL DEFAULT 1 CHECK (monitored IN (0, 1)), available_at INTEGER, available_confidence TEXT NOT NULL DEFAULT 'unknown'
  CHECK (available_confidence IN ('unknown', 'low', 'medium', 'high')), available_basis TEXT NOT NULL DEFAULT '', available_grace INTEGER,
  UNIQUE(work_id, media_ref)
);
CREATE INDEX work_part_order ON work_part(work_id, sort_key);
CREATE INDEX work_part_monitored ON work_part(work_id) WHERE monitored = 1;
CREATE INDEX work_part_available ON work_part(available_at) WHERE available_at IS NOT NULL;
CREATE INDEX work_part_date_title ON work_part(date, LOWER(title));

CREATE TABLE work_part_external_id (
  provider    TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  part_id     INTEGER NOT NULL REFERENCES work_part(id) ON DELETE CASCADE,
  confidence  REAL    NOT NULL DEFAULT 1.0,
  PRIMARY KEY (provider, external_id, part_id)
) WITHOUT ROWID;
CREATE INDEX work_part_external_by_part ON work_part_external_id(part_id);

CREATE TABLE work_part_title (
  id         INTEGER PRIMARY KEY,
  part_id    INTEGER NOT NULL REFERENCES work_part(id) ON DELETE CASCADE,
  title      TEXT    NOT NULL,
  normalized TEXT    NOT NULL,
  language   TEXT,
  provider   TEXT
);
CREATE INDEX work_part_title_norm ON work_part_title(normalized);
CREATE INDEX work_part_title_part ON work_part_title(part_id);

CREATE TABLE alt_numbering (
  part_id  INTEGER NOT NULL REFERENCES work_part(id) ON DELETE CASCADE,
  scheme   TEXT    NOT NULL,
  provider TEXT    NOT NULL,
  alt_ref  TEXT    NOT NULL,
  PRIMARY KEY (part_id, scheme, provider)
) WITHOUT ROWID;
CREATE INDEX alt_numbering_lookup ON alt_numbering(scheme, alt_ref, part_id);

CREATE TABLE edition (
  id               INTEGER PRIMARY KEY,
  work_id          INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  kind             TEXT    NOT NULL,
  label            TEXT    NOT NULL,
  year             INTEGER,
  format           TEXT,
  -- How a label identifies a pressing: "WARP CD 100".
  catalogue_number TEXT,
  -- UPC, EAN or ISBN-13.
  barcode          TEXT,
  -- Overrides work.runtime_ms when set: an extended cut runs longer (ADR-40).
  runtime_ms       INTEGER,
  is_default       INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
  UNIQUE(work_id, kind, label)
);
CREATE INDEX edition_by_work ON edition(work_id);

CREATE TABLE edition_external_id (
  provider    TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  edition_id  INTEGER NOT NULL REFERENCES edition(id) ON DELETE CASCADE,
  confidence  REAL    NOT NULL DEFAULT 1.0,
  PRIMARY KEY (provider, external_id)
) WITHOUT ROWID;
CREATE INDEX edition_external_by_edition ON edition_external_id(edition_id);

CREATE TABLE work_field (
  work_id    INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  field      TEXT    NOT NULL,
  provider   TEXT    NOT NULL,
  value      TEXT,
  fetched_at INTEGER NOT NULL,
  PRIMARY KEY (work_id, field, provider)
) WITHOUT ROWID;

CREATE TABLE media_attributes (
  id              INTEGER PRIMARY KEY,
  -- Deterministic hash of the normalised set.
  attr_hash       TEXT    NOT NULL UNIQUE,
  resolution      TEXT,
  source          TEXT,
  video_codec     TEXT,
  hdr             TEXT,
  audio_codec     TEXT,
  audio_channels  TEXT,
  bit_depth       INTEGER,
  -- JSON arrays.
  languages       TEXT,
  subtitles       TEXT,
  edition_tokens  TEXT,
  release_group   TEXT,
  proper_repack   INTEGER NOT NULL DEFAULT 0,
  frame_rate      REAL,
  scan_type       TEXT,
  container       TEXT,
  lossless        INTEGER,
  sample_rate     INTEGER
, image_format TEXT, colour_depth INTEGER, page_count INTEGER, bitrate_kbps INTEGER, bitrate_mode TEXT, vbr_preset TEXT, lyrics_languages TEXT, crf REAL);
CREATE INDEX media_attributes_res ON media_attributes(resolution, source);

CREATE TABLE media_object (
  id               INTEGER PRIMARY KEY,
  size_bytes       INTEGER NOT NULL,
  -- Size plus a head and tail sample: cheap identity without a full read.
  quick_hash       TEXT    NOT NULL,
  content_hash     TEXT,
  -- Exact identity with no decode (ADR-35).
  oshash           TEXT,
  -- 64-bit perceptual hash: same video content.
  phash            INTEGER,
  -- Chromaprint: same audio content across formats.
  audio_fp         BLOB,
  audio_fp_prefix  INTEGER,
  -- Null means still queued for background generation.
  fingerprinted_at INTEGER,
  -- Claims from the release name against facts from probing; facts win (ADR-39).
  claimed_attributes_id   INTEGER REFERENCES media_attributes(id),
  measured_attributes_id  INTEGER REFERENCES media_attributes(id),
  attribute_mismatch_json TEXT,
  -- Measured, and unique per file: interning these would give one attribute row per
  -- object and a join for every read. A release name states none of them, so they
  -- have no claimed counterpart to be compared against as a set.
  duration_ms      INTEGER,
  width            INTEGER,
  height           INTEGER,
  video_bitrate    INTEGER,
  audio_bitrate    INTEGER,
  overall_bitrate  INTEGER,
  probe_json       TEXT,
  first_seen_at    INTEGER NOT NULL,
  verified_at      INTEGER
, kind TEXT);
CREATE INDEX media_object_quick ON media_object(quick_hash);
CREATE INDEX media_object_oshash ON media_object(oshash) WHERE oshash IS NOT NULL;
CREATE INDEX media_object_afp ON media_object(audio_fp_prefix) WHERE audio_fp_prefix IS NOT NULL;
CREATE INDEX media_object_unfp ON media_object(id) WHERE fingerprinted_at IS NULL;
CREATE INDEX media_object_attrs ON media_object(measured_attributes_id);

CREATE TABLE library_path (
  id              INTEGER PRIMARY KEY,
  media_object_id INTEGER NOT NULL REFERENCES media_object(id) ON DELETE CASCADE,
  -- No foreign key: root_folder lives in settings.db (ADR-25, section 4.3).
  root_folder_id  INTEGER NOT NULL,
  rel_path        TEXT    NOT NULL,
  link_kind       TEXT    NOT NULL,
  is_primary      INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1)),
  created_at      INTEGER NOT NULL, last_seen_at INTEGER NOT NULL DEFAULT 0, missing_since INTEGER,
  UNIQUE(root_folder_id, rel_path)
);
CREATE INDEX library_path_object ON library_path(media_object_id);
CREATE INDEX library_path_missing_scan ON library_path(root_folder_id, missing_since, last_seen_at);

CREATE TABLE work_classification (
  work_id INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  -- 'primary' admits one value per work; 'secondary' admits many.
  kind    TEXT    NOT NULL,
  value   TEXT    NOT NULL,
  source  TEXT    NOT NULL DEFAULT '',
  PRIMARY KEY (work_id, kind, value)
) WITHOUT ROWID;
CREATE INDEX work_classification_lookup ON work_classification(kind, value);

CREATE TABLE desired_item_file (
  desired_item_id INTEGER NOT NULL REFERENCES desired_item(id) ON DELETE CASCADE,
  media_object_id INTEGER NOT NULL REFERENCES media_object(id) ON DELETE CASCADE,
  role            TEXT    NOT NULL DEFAULT 'primary',
  -- Which part of a SET this file covers; '' when the item is SINGLE. In the key,
  -- so one file can cover many parts: an audiobook whose twenty chapters are one
  -- file, or a trade paperback carrying six issues (Appendix E, D1).
  media_ref       TEXT    NOT NULL DEFAULT '',
  added_at        INTEGER NOT NULL,
  PRIMARY KEY (desired_item_id, media_object_id, role, media_ref)
) WITHOUT ROWID;
CREATE INDEX desired_file_by_object ON desired_item_file(media_object_id);
CREATE INDEX desired_file_primary ON desired_item_file(desired_item_id)
  WHERE role = 'primary';

CREATE TABLE request (
  id             INTEGER PRIMARY KEY,
  user_id        INTEGER NOT NULL,
  work_id        INTEGER REFERENCES work(id) ON DELETE CASCADE,
  -- Set when the work is not in the library yet.
  external_ref   TEXT,
  media_type     TEXT    NOT NULL,
  category_id    INTEGER,
  scope_json     TEXT,
  state          TEXT    NOT NULL
                 CHECK (state IN ('PENDING', 'APPROVED', 'DECLINED', 'FULFILLED')),
  decided_by     INTEGER,
  decided_at     INTEGER,
  decline_reason TEXT,
  created_at     INTEGER NOT NULL
);
CREATE INDEX request_pending ON request(state, created_at) WHERE state = 'PENDING';
CREATE INDEX request_by_user ON request(user_id, created_at DESC);

CREATE TABLE hunt_queue (
  desired_item_id  INTEGER PRIMARY KEY REFERENCES desired_item(id) ON DELETE CASCADE,
  class            TEXT    NOT NULL CHECK (class IN ('missing', 'upgrade')),
  priority         INTEGER NOT NULL,
  last_searched_at INTEGER NOT NULL DEFAULT 0,
  release_date     INTEGER,
  -- Availability and backoff, folded into one comparison.
  eligible_after   INTEGER NOT NULL DEFAULT 0,
  search_count     INTEGER NOT NULL DEFAULT 0
, queued_since INTEGER NOT NULL DEFAULT 0, after_failure INTEGER NOT NULL DEFAULT 0);
CREATE INDEX hunt_by_searched ON hunt_queue(class, eligible_after, priority DESC, last_searched_at ASC);
CREATE INDEX hunt_by_release ON hunt_queue(class, eligible_after, priority DESC, release_date ASC);

CREATE TABLE counter (
  scope      TEXT    NOT NULL,
  metric     TEXT    NOT NULL,
  value      INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (scope, metric)
) WITHOUT ROWID;

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
CREATE INDEX decision_by_candidate ON candidate_decision(candidate_id);

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

CREATE TABLE "recycle_bin" (
  id              INTEGER PRIMARY KEY,
  original_path   TEXT    NOT NULL,
  recycled_path TEXT    NOT NULL UNIQUE,
  root_folder_id  INTEGER,
  media_object_id INTEGER REFERENCES media_object(id) ON DELETE SET NULL,
  acquisition_id  INTEGER REFERENCES acquisition(id) ON DELETE SET NULL,
  reason          TEXT    NOT NULL,
  -- Counts against the free-space gate, so quarantine cannot cause the condition
  -- it exists to prevent (ADR-12).
  size_bytes      INTEGER NOT NULL,
  recycled_at  INTEGER NOT NULL,
  expires_at      INTEGER NOT NULL,
  restored_at     INTEGER
);
CREATE INDEX recycle_bin_expiry ON recycle_bin(expires_at) WHERE restored_at IS NULL;
CREATE INDEX recycle_bin_by_root ON recycle_bin(root_folder_id) WHERE restored_at IS NULL;

CREATE TABLE acquisition_file (
  id              INTEGER PRIMARY KEY,
  acquisition_id  INTEGER NOT NULL REFERENCES acquisition(id) ON DELETE CASCADE,
  source_path     TEXT    NOT NULL,
  media_object_id INTEGER REFERENCES media_object(id),
  library_path_id INTEGER REFERENCES library_path(id),
  imported_at     INTEGER
);
CREATE INDEX acq_file_object ON acquisition_file(media_object_id);

CREATE TABLE person (
  id             INTEGER PRIMARY KEY,
  name           TEXT    NOT NULL,
  sort_name      TEXT    NOT NULL,
  disambiguation TEXT,
  kind           TEXT,
  gender         TEXT,
  country        TEXT,
  -- Partial dates allowed: 1979, 1979-04, 1979-04-12.
  born           TEXT,
  died           TEXT,
  fields_json    TEXT,
  monitored      INTEGER NOT NULL DEFAULT 0 CHECK (monitored IN (0, 1)),
  added_at       INTEGER NOT NULL
);
CREATE INDEX person_monitored ON person(id) WHERE monitored = 1;
CREATE INDEX person_sort ON person(sort_name COLLATE NOCASE);
CREATE INDEX person_name_nocase ON person(name COLLATE NOCASE);

CREATE TABLE person_alias (
  person_id  INTEGER NOT NULL REFERENCES person(id) ON DELETE CASCADE,
  alias      TEXT    NOT NULL,
  normalized TEXT    NOT NULL,
  source     TEXT    NOT NULL,
  PRIMARY KEY (person_id, alias)
) WITHOUT ROWID;
CREATE INDEX person_alias_norm ON person_alias(normalized);

CREATE TABLE person_external_id (
  provider    TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  person_id   INTEGER NOT NULL REFERENCES person(id) ON DELETE CASCADE,
  PRIMARY KEY (provider, external_id)
) WITHOUT ROWID;
CREATE INDEX person_external_by_person ON person_external_id(person_id);

CREATE TABLE credit (
  person_id  INTEGER NOT NULL REFERENCES person(id) ON DELETE CASCADE,
  work_id    INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  edition_id INTEGER NOT NULL DEFAULT 0,
  part_id    INTEGER NOT NULL DEFAULT 0,
  role       TEXT    NOT NULL,
  character  TEXT    NOT NULL DEFAULT '',
  ord        INTEGER,
  PRIMARY KEY (person_id, work_id, edition_id, part_id, role, character)
);
CREATE INDEX credit_by_work ON credit(work_id, role);
CREATE INDEX credit_by_person ON credit(person_id, role);
CREATE INDEX credit_by_part ON credit(part_id, role) WHERE part_id != 0;
CREATE INDEX credit_by_edition ON credit(edition_id, role) WHERE edition_id != 0;

CREATE TABLE organisation (
  id          INTEGER PRIMARY KEY,
  name        TEXT    NOT NULL,
  sort_name   TEXT    NOT NULL,
  kind        TEXT    NOT NULL,
  -- Sub-labels, imprints and sub-sites are real in every domain.
  parent_id   INTEGER REFERENCES organisation(id),
  country     TEXT,
  fields_json TEXT,
  monitored   INTEGER NOT NULL DEFAULT 0 CHECK (monitored IN (0, 1))
);
CREATE INDEX organisation_parent ON organisation(parent_id);
CREATE INDEX organisation_sort ON organisation(sort_name COLLATE NOCASE);

CREATE TABLE organisation_external_id (
  provider        TEXT    NOT NULL,
  external_id     TEXT    NOT NULL,
  organisation_id INTEGER NOT NULL REFERENCES organisation(id) ON DELETE CASCADE,
  PRIMARY KEY (provider, external_id)
) WITHOUT ROWID;
CREATE INDEX organisation_external_by_org ON organisation_external_id(organisation_id);

CREATE TABLE work_organisation (
  work_id         INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  organisation_id INTEGER NOT NULL REFERENCES organisation(id) ON DELETE CASCADE,
  role            TEXT    NOT NULL,
  PRIMARY KEY (work_id, organisation_id, role)
) WITHOUT ROWID;
CREATE INDEX work_organisation_by_org ON work_organisation(organisation_id, role);

CREATE TABLE tag (
  id          INTEGER PRIMARY KEY,
  name        TEXT    NOT NULL,
  slug        TEXT    NOT NULL UNIQUE,
  parent_id   INTEGER REFERENCES tag(id),
  description TEXT,
  source      TEXT    NOT NULL,
  UNIQUE(parent_id, name)
);

CREATE TABLE tag_alias (
  tag_id INTEGER NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
  alias  TEXT    NOT NULL,
  PRIMARY KEY (tag_id, alias)
) WITHOUT ROWID;

CREATE TABLE tag_closure (
  ancestor_id   INTEGER NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
  descendant_id INTEGER NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
  depth         INTEGER NOT NULL,
  PRIMARY KEY (ancestor_id, descendant_id)
) WITHOUT ROWID;
CREATE INDEX tag_closure_desc ON tag_closure(descendant_id);

CREATE TABLE tag_assignment (
  tag_id       INTEGER NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
  subject_type TEXT    NOT NULL,
  subject_id   INTEGER NOT NULL,
  source       TEXT    NOT NULL,
  PRIMARY KEY (tag_id, subject_type, subject_id, source)
) WITHOUT ROWID;
CREATE INDEX tag_assignment_subject ON tag_assignment(subject_type, subject_id);

CREATE TABLE relationship (
  id        INTEGER PRIMARY KEY,
  from_type TEXT    NOT NULL,
  from_id   INTEGER NOT NULL,
  to_type   TEXT    NOT NULL,
  to_id     INTEGER NOT NULL,
  kind      TEXT    NOT NULL,
  UNIQUE(from_type, from_id, to_type, to_id, kind)
);
CREATE INDEX relationship_to ON relationship(to_type, to_id);

CREATE TABLE collection (
  id          INTEGER PRIMARY KEY,
  media_type  TEXT    NOT NULL,
  name        TEXT    NOT NULL,
  kind        TEXT    NOT NULL,
  source      TEXT    NOT NULL,
  monitored   INTEGER NOT NULL DEFAULT 0 CHECK (monitored IN (0, 1)),
  -- The member count a provider states, when it states one; null means unknown
  -- rather than zero, so "4 of 6" is distinguishable from "4 of unknown".
  complete_at INTEGER,
  added_at    INTEGER NOT NULL
, poster_url TEXT NOT NULL DEFAULT '', category_id INTEGER NOT NULL DEFAULT 0);
CREATE INDEX collection_monitored ON collection(id) WHERE monitored = 1;

CREATE TABLE collection_external_id (
  provider      TEXT    NOT NULL,
  external_id   TEXT    NOT NULL,
  collection_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
  PRIMARY KEY (provider, external_id)
) WITHOUT ROWID;

CREATE TABLE stat_rollup (
  metric      TEXT    NOT NULL,
  dimension   TEXT    NOT NULL DEFAULT '',
  -- Day bucket; 0 is the current snapshot.
  period      INTEGER NOT NULL,
  value       REAL    NOT NULL,
  count       INTEGER,
  computed_at INTEGER NOT NULL,
  PRIMARY KEY (metric, dimension, period)
) WITHOUT ROWID;
CREATE INDEX stat_rollup_metric ON stat_rollup(metric, period DESC);

CREATE TABLE artwork (
  id           INTEGER PRIMARY KEY,
  subject_type TEXT    NOT NULL,
  subject_id   INTEGER NOT NULL,
  kind         TEXT    NOT NULL,
  source_url   TEXT,
  cache_key    TEXT    NOT NULL,
  width        INTEGER,
  height       INTEGER,
  fetched_at   INTEGER,
  UNIQUE(subject_type, subject_id, kind)
);

CREATE TABLE group_reputation (
  release_group    TEXT    PRIMARY KEY,
  -- Decay-weighted, not a raw count.
  samples          INTEGER NOT NULL,
  raw_success      REAL,
  bounded_success  REAL,
  corruption_rate  REAL,
  -- Claimed against measured, from ADR-39.
  mislabel_rate    REAL,
  last_observed_at INTEGER,
  updated_at       INTEGER NOT NULL
) WITHOUT ROWID;

CREATE TABLE group_observation (
  id              INTEGER PRIMARY KEY,
  release_group   TEXT    NOT NULL,
  media_object_id INTEGER REFERENCES media_object(id) ON DELETE SET NULL,
  outcome         TEXT    NOT NULL
                  CHECK (outcome IN ('clean', 'corrupt', 'mislabelled',
                                     'wrong_content', 'unusable')),
  -- Only 'content' counts toward reputation: a full disk is not the group's fault.
  cause           TEXT    NOT NULL CHECK (cause IN ('content', 'environment', 'user')),
  observed_at     INTEGER NOT NULL,
  was_exploration INTEGER NOT NULL DEFAULT 0 CHECK (was_exploration IN (0, 1))
);
CREATE INDEX group_observation_grp ON group_observation(release_group, observed_at DESC);

CREATE TABLE decision_rollup (
  desired_item_id INTEGER NOT NULL REFERENCES desired_item(id) ON DELETE CASCADE,
  -- Day bucket, as a Unix day number.
  period          INTEGER NOT NULL,
  outcome         TEXT    NOT NULL,
  count           INTEGER NOT NULL,
  best_score      REAL,
  -- The rejection rule that fired most often in the bucket.
  top_reason      TEXT,
  PRIMARY KEY (desired_item_id, period, outcome)
) WITHOUT ROWID;
CREATE INDEX decision_rollup_period ON decision_rollup(period, outcome);

CREATE VIRTUAL TABLE title_search USING fts5(
  title,
  work_id UNINDEXED,
  media_type UNINDEXED,
  tokenize = 'unicode61 remove_diacritics 2'
);

CREATE TABLE scan_state (
  -- No foreign key: root_folder lives in settings.db (ADR-25, section 4.3).
  root_folder_id INTEGER NOT NULL PRIMARY KEY,
  -- The wall-clock time the last completed sweep started, as Unix seconds. The
  -- start, never the end: a directory written after the walk had passed it would
  -- otherwise sit behind the watermark and never be read again
  -- (documentation/database-layout.md). Zero means nothing has completed yet.
  swept_through  INTEGER NOT NULL DEFAULT 0,
  -- The last directory committed by an interrupted sweep, relative to the root.
  -- Empty means there is nothing to resume. Compared segment by segment, never as
  -- a string: walk order puts 'a/c' before 'a-b'.
  cursor         TEXT    NOT NULL DEFAULT '',
  -- When the sweep in progress began, and when the last one finished, so an
  -- operator can see a scan that has stalled.
  started_at     INTEGER,
  finished_at    INTEGER,
  -- Counters from the last completed sweep, for the same reason.
  files_found    INTEGER NOT NULL DEFAULT 0,
  directories_read INTEGER NOT NULL DEFAULT 0
, vanished_held INTEGER NOT NULL DEFAULT 0, vanished_through INTEGER NOT NULL DEFAULT 0) WITHOUT ROWID;

CREATE TABLE work_root_folder (
  work_id        INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  -- No foreign key: root_folder lives in settings.db (ADR-25, section 4.3).
  root_folder_id INTEGER NOT NULL,
  first_seen_at  INTEGER NOT NULL,
  PRIMARY KEY (work_id, root_folder_id)
) WITHOUT ROWID;
CREATE INDEX work_root_folder_by_root ON work_root_folder(root_folder_id);

CREATE TABLE identification_queue (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  -- No foreign key: root_folder lives in settings.db (ADR-25, section 4.3), same as
  -- library_path.root_folder_id.
  root_folder_id  INTEGER NOT NULL,
  rel_path        TEXT    NOT NULL,
  candidates_json TEXT    NOT NULL,
  first_seen_at   INTEGER NOT NULL,
  last_seen_at    INTEGER NOT NULL
);
CREATE INDEX identification_queue_root ON identification_queue(root_folder_id);
CREATE INDEX identification_queue_cursor ON identification_queue(first_seen_at, media_object_id);

CREATE TABLE import_session (
  id                INTEGER PRIMARY KEY,
  source_path       TEXT    NOT NULL,
  origin            TEXT    NOT NULL,     -- manual | acquisition | watched | adoption
  acquisition_id    INTEGER,              -- set when origin = acquisition
  category_id       INTEGER,              -- target; NULL means "decide per unit". No FK:
                                          -- category lives in settings.db (ADR-25).
  state             TEXT    NOT NULL,     -- INSPECTING | IMPORTING | DONE | FAILED | REVERTED
  threshold         REAL    NOT NULL,     -- the value in force for this run, recorded
  units_total       INTEGER NOT NULL DEFAULT 0,
  units_committed   INTEGER NOT NULL DEFAULT 0,
  units_queued      INTEGER NOT NULL DEFAULT 0,
  bytes_imported    INTEGER NOT NULL DEFAULT 0,
  extract_dir       TEXT,                 -- workspace, removed when the session ends
  started_at        INTEGER NOT NULL,
  finished_at       INTEGER,
  error             TEXT
, units_skipped INTEGER NOT NULL DEFAULT 0, units_failed  INTEGER NOT NULL DEFAULT 0, units_estimated INTEGER, dismissed_at INTEGER, preview INTEGER NOT NULL DEFAULT 0 CHECK (preview IN (0, 1)));
CREATE INDEX idx_import_session_state ON import_session(state, started_at DESC);

CREATE TABLE import_unit (
  id                INTEGER PRIMARY KEY,
  session_id        INTEGER NOT NULL REFERENCES import_session(id) ON DELETE CASCADE,
  rel_path          TEXT    NOT NULL,     -- the unit's own root inside source_path
  shape             TEXT    NOT NULL,     -- SINGLE | SET | PACK | MULTI_DISC
  file_count        INTEGER NOT NULL,
  bytes             INTEGER NOT NULL,
  confidence        REAL    NOT NULL,
  decision          TEXT    NOT NULL,     -- COMMIT | QUEUE | SKIP | FAILED
  work_id           INTEGER REFERENCES work(id),
  edition_id        INTEGER REFERENCES edition(id),
  evidence_json     TEXT    NOT NULL,     -- every signal and what it said (ADR-10 shape)
  candidates_json   TEXT,                 -- ranked alternatives, for the queue
  reason            TEXT                  -- structured code when QUEUE or SKIP
, work_created    INTEGER NOT NULL DEFAULT 0, edition_created INTEGER NOT NULL DEFAULT 0, pack_id INTEGER REFERENCES pack(id));
CREATE INDEX idx_import_unit_session ON import_unit(session_id, decision);
CREATE INDEX idx_import_unit_queue   ON import_unit(decision, confidence DESC)
  WHERE decision = 'QUEUE';
CREATE INDEX idx_import_unit_pack ON import_unit(pack_id) WHERE pack_id IS NOT NULL;

CREATE TABLE blocked_content (
  id             INTEGER PRIMARY KEY,
  oshash         TEXT,                   -- fast, size+head+tail; the stage-4 identifier
  quick_hash     TEXT,                   -- sampled content hash
  chromaprint    TEXT,                   -- audio, once stage 7 lands (ADR-35)
  phash          TEXT,                   -- video, once stage 8 lands
  size_bytes     INTEGER NOT NULL,
  reason         TEXT    NOT NULL,       -- FAKE | MISLABELLED | CORRUPT | UNWANTED
                                         -- | EXECUTABLE
  note           TEXT,                   -- the user's own words; shown wherever it fires
  media_type     TEXT,                   -- NULL = every media type
  title          TEXT    NOT NULL DEFAULT '',   -- what it claimed to be, for the UI
  release_group  TEXT    NOT NULL DEFAULT '',   -- feeds reputation only when source = user
  source         TEXT    NOT NULL,       -- user | auto
  user_id        INTEGER,
  expires_at     INTEGER,                -- NULL = permanent; manual blocks are permanent
  created_at     INTEGER NOT NULL,
  hit_count      INTEGER NOT NULL DEFAULT 0,
  last_hit_at    INTEGER,
  CHECK (oshash IS NOT NULL OR quick_hash IS NOT NULL
         OR chromaprint IS NOT NULL OR phash IS NOT NULL)
);
CREATE INDEX blocked_by_oshash     ON blocked_content(oshash)      WHERE oshash IS NOT NULL;
CREATE INDEX blocked_by_quick_hash ON blocked_content(quick_hash)  WHERE quick_hash IS NOT NULL;
CREATE INDEX blocked_by_chromaprint ON blocked_content(chromaprint)
  WHERE chromaprint IS NOT NULL;

CREATE TABLE move_job (
  id              INTEGER PRIMARY KEY,
  reason          TEXT    NOT NULL,       -- RENAME | CATEGORY | REBALANCE | DRAIN
  source_root_id  INTEGER,                -- set for DRAIN
  state           TEXT    NOT NULL,       -- PLANNED | RUNNING | PAUSED | DONE | FAILED
  files_total     INTEGER NOT NULL,
  files_done      INTEGER NOT NULL DEFAULT 0,
  bytes_total     INTEGER NOT NULL,
  bytes_done      INTEGER NOT NULL DEFAULT 0,
  plan_json       TEXT    NOT NULL,       -- the previewed plan, exactly as accepted
  created_at      INTEGER NOT NULL,
  finished_at     INTEGER,
  error           TEXT
, category_id INTEGER);
CREATE INDEX idx_move_job_state ON move_job(state, created_at DESC);

CREATE TABLE move_item (
  move_job_id     INTEGER NOT NULL REFERENCES move_job(id) ON DELETE CASCADE,
  media_object_id INTEGER NOT NULL,
  affinity_key    TEXT    NOT NULL DEFAULT '',
  dest_root_id    INTEGER NOT NULL,
  dest_rel_path   TEXT    NOT NULL,
  bytes           INTEGER NOT NULL,
  state           TEXT    NOT NULL,       -- PENDING | COPIED | VERIFIED | DONE | FAILED
  error           TEXT,
  PRIMARY KEY (move_job_id, media_object_id)
) WITHOUT ROWID;
CREATE INDEX idx_move_item_pending ON move_item(move_job_id, state);

CREATE TABLE import_unit_file (
  id              INTEGER PRIMARY KEY,
  unit_id         INTEGER NOT NULL REFERENCES import_unit(id) ON DELETE CASCADE,
  media_object_id INTEGER NOT NULL REFERENCES media_object(id) ON DELETE CASCADE
, library_path_id INTEGER REFERENCES library_path(id) ON DELETE SET NULL, transfer_method TEXT);
CREATE INDEX idx_import_unit_file_unit   ON import_unit_file(unit_id);
CREATE INDEX idx_import_unit_file_object ON import_unit_file(media_object_id);

CREATE TABLE pack (
  id              INTEGER PRIMARY KEY,
  kind            TEXT    NOT NULL,       -- DISCOGRAPHY | BOX_SET | ANTHOLOGY | COMPILATION
  detected_from   TEXT    NOT NULL,       -- structure | structure_and_name
  confidence      REAL    NOT NULL,
  title           TEXT    NOT NULL DEFAULT '',   -- as released, for display only
  acquisition_id  INTEGER,                -- set when it arrived by acquisition
  session_id      INTEGER REFERENCES import_session(id),
  member_count    INTEGER NOT NULL DEFAULT 0,
  resolved_count  INTEGER NOT NULL DEFAULT 0,    -- members that identified and placed
  created_at      INTEGER NOT NULL
);
CREATE INDEX idx_pack_session ON pack(session_id);

CREATE TABLE pack_item (
  pack_id         INTEGER NOT NULL REFERENCES pack(id) ON DELETE CASCADE,
  ord             INTEGER NOT NULL,
  work_id         INTEGER REFERENCES work(id),
  edition_id      INTEGER REFERENCES edition(id),
  import_unit_id  INTEGER REFERENCES import_unit(id),
  state           TEXT    NOT NULL,       -- RESOLVED | QUEUED | FAILED | SKIPPED
  PRIMARY KEY (pack_id, ord)
) WITHOUT ROWID;
CREATE INDEX idx_pack_item_work ON pack_item(work_id);

CREATE TABLE arr_import (
  id               INTEGER PRIMARY KEY,
  instance_id      INTEGER NOT NULL,
  app              TEXT    NOT NULL,
  state            TEXT    NOT NULL,     -- RUNNING | DONE | FAILED | REVERTED
  started_at       INTEGER NOT NULL,
  finished_at      INTEGER,
  error            TEXT,
  artists_created  INTEGER NOT NULL DEFAULT 0,
  artists_matched  INTEGER NOT NULL DEFAULT 0,
  albums_created   INTEGER NOT NULL DEFAULT 0,
  albums_matched   INTEGER NOT NULL DEFAULT 0,
  items_parked     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_arr_import_instance ON arr_import(instance_id);

CREATE TABLE arr_import_item (
  id                    INTEGER PRIMARY KEY,
  import_id             INTEGER NOT NULL REFERENCES arr_import(id) ON DELETE CASCADE,
  kind                  TEXT    NOT NULL,     -- artist | album | parked
  source_id             TEXT    NOT NULL,     -- the *Arr app's own id for this entity
  title                 TEXT    NOT NULL,
  person_id             INTEGER REFERENCES person(id) ON DELETE SET NULL,
  person_created        INTEGER NOT NULL DEFAULT 0 CHECK (person_created IN (0, 1)),
  subscription_id       INTEGER REFERENCES subscription(id) ON DELETE SET NULL,
  work_id               INTEGER REFERENCES work(id) ON DELETE SET NULL,
  work_created          INTEGER NOT NULL DEFAULT 0 CHECK (work_created IN (0, 1)),
  desired_item_id       INTEGER REFERENCES desired_item(id) ON DELETE SET NULL,
  desired_item_created  INTEGER NOT NULL DEFAULT 0 CHECK (desired_item_created IN (0, 1)),
  payload_json          TEXT,
  created_at            INTEGER NOT NULL
);
CREATE INDEX idx_arr_import_item_import ON arr_import_item(import_id);

CREATE TABLE "desired_item" (
  id                   INTEGER PRIMARY KEY,
  work_id              INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  edition_id           INTEGER REFERENCES edition(id),
  media_ref            TEXT    NOT NULL DEFAULT '',
  media_sort_key       TEXT    NOT NULL DEFAULT '',
  category_id          INTEGER NOT NULL,
  profile_id           INTEGER NOT NULL,
  monitored            INTEGER NOT NULL DEFAULT 1 CHECK (monitored IN (0, 1)),

  satisfaction_kind    TEXT    NOT NULL DEFAULT 'SINGLE'
                       CHECK (satisfaction_kind IN ('SINGLE', 'SET')),
  required_count       INTEGER,
  present_count        INTEGER NOT NULL DEFAULT 0,
  state                TEXT    NOT NULL
                       CHECK (state IN ('MISSING', 'PARTIAL', 'COMPLETE', 'UPGRADABLE')),

  available_from       INTEGER,
  available_confidence TEXT,
  available_basis      TEXT,

  dirty                INTEGER NOT NULL DEFAULT 1 CHECK (dirty IN (0, 1)),
  generation           INTEGER NOT NULL DEFAULT 0,
  last_planned_at      INTEGER,
  added_by             INTEGER,
  UNIQUE(work_id, edition_id, media_ref, profile_id)
);
CREATE INDEX desired_dirty ON desired_item(id) WHERE dirty = 1;
CREATE INDEX desired_active ON desired_item(state, work_id) WHERE monitored = 1;
CREATE INDEX desired_by_work ON desired_item(work_id, media_sort_key);
CREATE INDEX desired_by_category ON desired_item(category_id, state);
CREATE INDEX desired_monitored_by_work ON desired_item(work_id, id) WHERE monitored = 1;
CREATE INDEX desired_by_work_category ON desired_item(work_id, category_id);

CREATE TABLE work_file (
  work_id         INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  media_object_id INTEGER NOT NULL REFERENCES media_object(id) ON DELETE CASCADE,
  first_seen_at   INTEGER NOT NULL,
  PRIMARY KEY (work_id, media_object_id)
) WITHOUT ROWID;
CREATE INDEX work_file_object ON work_file(media_object_id);

CREATE TABLE identification_attempt (
  id              INTEGER PRIMARY KEY,
  media_object_id INTEGER NOT NULL REFERENCES media_object(id) ON DELETE CASCADE,
  attempted_at    INTEGER NOT NULL,
  outcome         TEXT    NOT NULL,
  passes_json     TEXT    NOT NULL
);
CREATE INDEX identification_attempt_object ON identification_attempt(media_object_id, attempted_at, id);

CREATE TABLE import_unit_extra (
  id             INTEGER PRIMARY KEY,
  unit_id        INTEGER NOT NULL REFERENCES import_unit(id) ON DELETE CASCADE,
  root_folder_id INTEGER NOT NULL,
  rel_path       TEXT    NOT NULL
);
CREATE INDEX idx_import_unit_extra_unit ON import_unit_extra(unit_id);

CREATE TABLE not_media (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  marked_at       INTEGER NOT NULL
);

CREATE TABLE manual_identification (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  work_id         INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  decided_at      INTEGER NOT NULL
);

CREATE TABLE person_refresh (
  person_id    INTEGER PRIMARY KEY REFERENCES person(id) ON DELETE CASCADE,
  refreshed_at INTEGER NOT NULL
);

CREATE TABLE file_track_claim (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  disc            INTEGER NOT NULL DEFAULT 0,
  track           INTEGER NOT NULL DEFAULT 0,
  title           TEXT    NOT NULL DEFAULT '',
  recording_mbid  TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE tag_writeback (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  attempted_at    INTEGER NOT NULL,
  -- The keys written, comma-separated; empty when the file already had them all.
  written         TEXT    NOT NULL DEFAULT '',
  error           TEXT
, digest TEXT NOT NULL DEFAULT '');

CREATE TABLE work_arrival (
  work_id    INTEGER PRIMARY KEY REFERENCES work(id) ON DELETE CASCADE,
  arrived_at INTEGER NOT NULL
);
CREATE INDEX work_arrival_at ON work_arrival(arrived_at, work_id);

CREATE TABLE desired_profile_override (
  desired_item_id INTEGER PRIMARY KEY REFERENCES desired_item(id) ON DELETE CASCADE,
  profile_id      INTEGER NOT NULL
) WITHOUT ROWID;

CREATE TABLE "collection_member" (
  collection_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
  work_id       INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  part_id       INTEGER NOT NULL DEFAULT 0,
  ord           INTEGER,
  PRIMARY KEY (collection_id, work_id, part_id)
) WITHOUT ROWID;
CREATE INDEX collection_member_work ON collection_member(work_id);

CREATE TABLE work_release_filter (
  id               INTEGER PRIMARY KEY,
  work_id          INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  media_ref_prefix TEXT    NOT NULL DEFAULT '',
  kind             TEXT    NOT NULL CHECK (kind IN ('must_contain', 'must_not_contain', 'prefer', 'group_allow', 'group_deny')),
  value            TEXT    NOT NULL,
  is_regex         INTEGER NOT NULL DEFAULT 0 CHECK (is_regex IN (0, 1)),
  -- Additive weight for 'prefer'; ignored by the other kinds.
  score            INTEGER NOT NULL DEFAULT 0,
  created_at       INTEGER NOT NULL,
  UNIQUE (work_id, media_ref_prefix, kind, value)
);
CREATE INDEX work_release_filter_work ON work_release_filter(work_id);

CREATE TABLE person_library (
  person_id   INTEGER NOT NULL REFERENCES person(id) ON DELETE CASCADE,
  category_id INTEGER NOT NULL,
  role        TEXT    NOT NULL,
  works       INTEGER NOT NULL,
  latest      INTEGER,
  PRIMARY KEY (person_id, category_id, role)
) WITHOUT ROWID;
CREATE INDEX person_library_by_category ON person_library(category_id, role, person_id);

CREATE TABLE organisation_library (
  organisation_id INTEGER NOT NULL REFERENCES organisation(id) ON DELETE CASCADE,
  category_id     INTEGER NOT NULL,
  role            TEXT    NOT NULL,
  works           INTEGER NOT NULL,
  latest          INTEGER,
  PRIMARY KEY (organisation_id, category_id, role)
) WITHOUT ROWID;
CREATE INDEX organisation_library_by_category ON organisation_library(category_id, role, organisation_id);

CREATE TABLE work_exclusion (
  media_type  TEXT    NOT NULL,
  provider    TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  title       TEXT    NOT NULL,
  created_at  INTEGER NOT NULL,
  PRIMARY KEY (provider, external_id)
) WITHOUT ROWID;

CREATE TABLE "subscription" (
  id                     INTEGER PRIMARY KEY,
  subject_type           TEXT    NOT NULL
                         CHECK (subject_type IN ('person', 'organisation', 'tag')),
  subject_id             INTEGER NOT NULL,
  media_types_json       TEXT    NOT NULL,
  roles_json             TEXT,
  category_id            INTEGER,
  profile_id             INTEGER,
  filters_json           TEXT,
  monitor_back_catalogue INTEGER NOT NULL DEFAULT 0
                         CHECK (monitor_back_catalogue IN (0, 1)),
  enabled                INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  user_id                INTEGER,
  created_at             INTEGER NOT NULL,
  UNIQUE(subject_type, subject_id, user_id, category_id)
);
CREATE INDEX subscription_subject ON subscription(subject_type, subject_id) WHERE enabled = 1;

CREATE TABLE "ready_queue" (
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
CREATE INDEX ready_queue_order ON ready_queue(priority DESC, queued_at ASC);

CREATE TABLE "acquisition" (
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
CREATE UNIQUE INDEX acquisition_one_inflight ON acquisition(desired_item_id)
  WHERE state NOT IN ('IMPORTED', 'CLEANED', 'FAILED', 'CANCELLED');
CREATE INDEX acquisition_active ON acquisition(state)
  WHERE state NOT IN ('IMPORTED', 'CLEANED', 'FAILED', 'CANCELLED');

CREATE TABLE rss_sighting (
  desired_item_id INTEGER PRIMARY KEY REFERENCES desired_item(id) ON DELETE CASCADE,
  seen_at         INTEGER NOT NULL
);
CREATE INDEX rss_sighting_seen ON rss_sighting(seen_at);

CREATE TABLE media_probe (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  probe_version   INTEGER NOT NULL,
  probed_at       INTEGER NOT NULL,
  json            TEXT    NOT NULL
);
CREATE INDEX media_probe_version ON media_probe(probe_version, media_object_id);

CREATE TABLE work_merge_suggestion (
  work_id      INTEGER PRIMARY KEY REFERENCES work(id) ON DELETE CASCADE,
  into_work_id INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  found_at     INTEGER NOT NULL
);
CREATE INDEX work_merge_suggestion_into ON work_merge_suggestion(into_work_id);

CREATE TABLE acquisition_stall (
  acquisition_id INTEGER PRIMARY KEY REFERENCES acquisition(id) ON DELETE CASCADE,
  strikes        INTEGER NOT NULL DEFAULT 0,
  left_bytes     INTEGER NOT NULL,
  progress_at    INTEGER NOT NULL,
  checked_at     INTEGER NOT NULL
, importing_finished INTEGER NOT NULL DEFAULT 0 CHECK (importing_finished IN (0, 1)));

CREATE TABLE work_franchise_check (
  work_id    INTEGER PRIMARY KEY REFERENCES work(id) ON DELETE CASCADE,
  checked_at INTEGER NOT NULL
);

CREATE TABLE work_anime_check (
  work_id    INTEGER PRIMARY KEY REFERENCES work(id) ON DELETE CASCADE,
  checked_at INTEGER NOT NULL
);

CREATE TRIGGER title_search_work_ai AFTER INSERT ON work BEGIN
  INSERT INTO title_search(rowid, title, work_id, media_type)
  VALUES (NEW.id * 4, NEW.canonical_title, NEW.id, NEW.media_type);
END;

CREATE TRIGGER title_search_work_au AFTER UPDATE OF canonical_title, media_type ON work BEGIN
  UPDATE title_search SET title = NEW.canonical_title, media_type = NEW.media_type
  WHERE rowid = NEW.id * 4;
END;

CREATE TRIGGER title_search_work_ad AFTER DELETE ON work BEGIN
  DELETE FROM title_search WHERE rowid = OLD.id * 4;
END;

CREATE TRIGGER title_search_work_title_ai AFTER INSERT ON work_title BEGIN
  INSERT INTO title_search(rowid, title, work_id, media_type)
  SELECT NEW.id * 4 + 1, NEW.title, NEW.work_id, w.media_type
  FROM work w WHERE w.id = NEW.work_id;
END;

CREATE TRIGGER title_search_work_title_au AFTER UPDATE OF title ON work_title BEGIN
  UPDATE title_search SET title = NEW.title WHERE rowid = NEW.id * 4 + 1;
END;

CREATE TRIGGER title_search_work_title_ad AFTER DELETE ON work_title BEGIN
  DELETE FROM title_search WHERE rowid = OLD.id * 4 + 1;
END;

CREATE TRIGGER title_search_work_part_ai AFTER INSERT ON work_part
WHEN NEW.title IS NOT NULL BEGIN
  INSERT INTO title_search(rowid, title, work_id, media_type)
  SELECT NEW.id * 4 + 2, NEW.title, NEW.work_id, w.media_type
  FROM work w WHERE w.id = NEW.work_id;
END;

CREATE TRIGGER title_search_work_part_au AFTER UPDATE OF title ON work_part BEGIN
  DELETE FROM title_search WHERE rowid = NEW.id * 4 + 2;
  INSERT INTO title_search(rowid, title, work_id, media_type)
  SELECT NEW.id * 4 + 2, NEW.title, NEW.work_id, w.media_type
  FROM work w WHERE w.id = NEW.work_id AND NEW.title IS NOT NULL;
END;

CREATE TRIGGER title_search_work_part_ad AFTER DELETE ON work_part BEGIN
  DELETE FROM title_search WHERE rowid = OLD.id * 4 + 2;
END;

CREATE TRIGGER title_search_work_part_title_ai AFTER INSERT ON work_part_title BEGIN
  INSERT INTO title_search(rowid, title, work_id, media_type)
  SELECT NEW.id * 4 + 3, NEW.title, p.work_id, w.media_type
  FROM work_part p JOIN work w ON w.id = p.work_id WHERE p.id = NEW.part_id;
END;

CREATE TRIGGER title_search_work_part_title_au AFTER UPDATE OF title ON work_part_title BEGIN
  UPDATE title_search SET title = NEW.title WHERE rowid = NEW.id * 4 + 3;
END;

CREATE TRIGGER title_search_work_part_title_ad AFTER DELETE ON work_part_title BEGIN
  DELETE FROM title_search WHERE rowid = OLD.id * 4 + 3;
END;

CREATE TRIGGER work_file_arrival AFTER INSERT ON work_file
BEGIN
  INSERT INTO work_arrival (work_id, arrived_at) VALUES (NEW.work_id, NEW.first_seen_at)
  ON CONFLICT (work_id) DO UPDATE SET arrived_at = MAX(arrived_at, excluded.arrived_at);
END;
