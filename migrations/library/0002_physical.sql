-- Physical files: bytes, the paths that reference them, and interned attributes
-- (section 5.2). media_attributes first, so the references below resolve.

-- Distinct combinations number in the thousands even at 500k files, so interning
-- saves space and makes "all my 2160p files" a small-table scan.
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
  -- Measured only: null on a claimed set, populated after probing (ADR-39).
  video_bitrate   INTEGER,
  audio_bitrate   INTEGER,
  overall_bitrate INTEGER,
  duration_ms     INTEGER,
  width           INTEGER,
  height          INTEGER,
  frame_rate      REAL,
  scan_type       TEXT,
  container       TEXT,
  lossless        INTEGER,
  sample_rate     INTEGER
);
CREATE INDEX media_attributes_res ON media_attributes(resolution, source);

-- One row per set of bytes; many paths may reference it (ADR-7, ADR-11).
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
  probe_json       TEXT,
  first_seen_at    INTEGER NOT NULL,
  verified_at      INTEGER
);
CREATE INDEX media_object_quick ON media_object(quick_hash);
CREATE INDEX media_object_oshash ON media_object(oshash) WHERE oshash IS NOT NULL;
CREATE INDEX media_object_afp ON media_object(audio_fp_prefix) WHERE audio_fp_prefix IS NOT NULL;
CREATE INDEX media_object_unfp ON media_object(id) WHERE fingerprinted_at IS NULL;
CREATE INDEX media_object_attrs ON media_object(measured_attributes_id);
-- No index on phash: matching is by Hamming distance, not equality. At 500k files
-- the whole set is 4 MB, loaded into memory and brute-forced in Go (ADR-35).

CREATE TABLE library_path (
  id              INTEGER PRIMARY KEY,
  media_object_id INTEGER NOT NULL REFERENCES media_object(id) ON DELETE CASCADE,
  -- No foreign key: root_folder lives in settings.db (ADR-25, section 4.3).
  root_folder_id  INTEGER NOT NULL,
  rel_path        TEXT    NOT NULL,
  link_kind       TEXT    NOT NULL,
  is_primary      INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1)),
  created_at      INTEGER NOT NULL,
  UNIQUE(root_folder_id, rel_path)
);
CREATE INDEX library_path_object ON library_path(media_object_id);
