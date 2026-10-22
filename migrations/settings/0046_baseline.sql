-- Every migration up to 0046, squashed into the schema they built (documentation/database-layout.md).
-- migrate:baseline replaces=8a48ce70dc41beb77a04591ec49e53d160944443e7fec76a8d825bcf12be810e

CREATE TABLE app_user (
  id            INTEGER PRIMARY KEY,
  username      TEXT    NOT NULL UNIQUE,
  -- Null for an account that authenticates by proxy header or API key only.
  password_hash TEXT,
  role          TEXT    NOT NULL CHECK (role IN ('admin', 'operator', 'readonly')),
  created_at    INTEGER NOT NULL,
  -- Which settings this user is shown (ADR-26). Raising the tier reveals
  -- settings; it never changes behaviour.
  ui_tier       TEXT    NOT NULL DEFAULT 'normal'
                        CHECK (ui_tier IN ('normal', 'advanced', 'expert'))
, ui_tier_default TEXT NOT NULL DEFAULT 'normal'
  CHECK (ui_tier_default IN ('normal', 'advanced', 'expert')), ui_tier_max TEXT NOT NULL DEFAULT 'expert'
  CHECK (ui_tier_max IN ('normal', 'advanced', 'expert')), new_library_permission TEXT NOT NULL DEFAULT 'none'
  CHECK (new_library_permission IN ('none', 'read', 'request', 'manage')));

CREATE TABLE root_folder (
  id     INTEGER PRIMARY KEY,
  path   TEXT    NOT NULL UNIQUE,
  -- Shown in the UI: "music-hdd-1".
  label  TEXT,
  -- An unmounted root counts as unavailable, never as free space (Invariant 20a).
  online INTEGER NOT NULL DEFAULT 1 CHECK (online IN (0, 1))
, writable INTEGER NOT NULL DEFAULT 1 CHECK (writable IN (0, 1)), writable_reason TEXT, read_only INTEGER NOT NULL DEFAULT 0 CHECK (read_only IN (0, 1)), allow_rename INTEGER NOT NULL DEFAULT 0 CHECK (allow_rename IN (0, 1)), total_bytes INTEGER, free_bytes INTEGER, min_free_bytes INTEGER NOT NULL DEFAULT 0, capacity_checked_at INTEGER, fs_id TEXT, capabilities_json TEXT, drain_state TEXT NOT NULL DEFAULT 'NONE'
  CHECK (drain_state IN ('NONE', 'DRAINING', 'PAUSED', 'DRAINED')), drain_job_id INTEGER);

CREATE TABLE category (
  id            INTEGER PRIMARY KEY,
  media_type    TEXT    NOT NULL,
  name          TEXT    NOT NULL,
  -- User-ordered rank feeding the composite acquisition priority (ADR-36).
  -- Lower is more important.
  priority_rank INTEGER NOT NULL DEFAULT 100, quality_profile_id INTEGER REFERENCES quality_profile(id), language_profile_id INTEGER REFERENCES language_profile(id), monitor_discovered TEXT NOT NULL DEFAULT 'monitored'
  CHECK (monitor_discovered IN ('monitored', 'unmonitored', 'none')), naming_profile_id INTEGER REFERENCES naming_profile(id), monitor_release_types TEXT NOT NULL
  DEFAULT '["Album","EP","Single"]', quality_cutoff_profile_id INTEGER REFERENCES quality_profile(id), quality_upgrades INTEGER NOT NULL DEFAULT 1 CHECK (quality_upgrades IN (0, 1)), preferred_protocol TEXT NOT NULL DEFAULT '' CHECK (preferred_protocol IN ('', 'usenet', 'torrent')), protocol_delay_minutes INTEGER NOT NULL DEFAULT 0 CHECK (protocol_delay_minutes >= 0), tv_monitor TEXT NOT NULL DEFAULT 'existing' CHECK (tv_monitor IN ('existing', 'all', 'none')), kind TEXT NOT NULL DEFAULT '',
  UNIQUE (media_type, name)
);

CREATE TABLE category_root_folder (
  category_id    INTEGER NOT NULL REFERENCES category(id),
  root_folder_id INTEGER NOT NULL REFERENCES root_folder(id),
  ord            INTEGER NOT NULL DEFAULT 0,
  weight         INTEGER NOT NULL DEFAULT 1,
  accepts_new    INTEGER NOT NULL DEFAULT 1 CHECK (accepts_new IN (0, 1)),
  PRIMARY KEY (category_id, root_folder_id)
) WITHOUT ROWID;
CREATE INDEX category_root_by_root ON category_root_folder(root_folder_id);

CREATE TABLE user_category_access (
  user_id     INTEGER NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
  category_id INTEGER NOT NULL REFERENCES category(id),
  permission  TEXT    NOT NULL
                      CHECK (permission IN ('none', 'read', 'request', 'manage')),
  PRIMARY KEY (user_id, category_id)
) WITHOUT ROWID;
CREATE INDEX user_category_access_by_category ON user_category_access(category_id);

CREATE TABLE user_preference (
  user_id    INTEGER NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
  key        TEXT    NOT NULL,
  value_json TEXT    NOT NULL,
  PRIMARY KEY (user_id, key)
) WITHOUT ROWID;

CREATE TABLE api_key (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
  name         TEXT    NOT NULL,
  key_hash     TEXT    NOT NULL UNIQUE,
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER
);
CREATE INDEX api_key_by_user ON api_key(user_id);

CREATE TABLE instance_setting (
  key        TEXT    NOT NULL PRIMARY KEY,
  value_json TEXT    NOT NULL,
  updated_at INTEGER NOT NULL
) WITHOUT ROWID;

CREATE TABLE discovery_job (
  id            INTEGER PRIMARY KEY,
  name          TEXT    NOT NULL UNIQUE,
  -- {kind: library|subscription|list, provider, list}
  source_json   TEXT    NOT NULL,
  media_type    TEXT,
  category_id   INTEGER REFERENCES category(id),
  -- ratings, votes, year, genre, language, tags, people
  filters_json  TEXT    NOT NULL,
  -- weights across rating sources, e.g. {imdb: 0.4, tmdb: 0.3, trakt: 0.3}
  scoring_json  TEXT,
  -- Top-N per run, never all-or-nothing -- a misconfigured job cannot add
  -- everything a list holds in one pass.
  limit_per_run INTEGER NOT NULL DEFAULT 10,
  action        TEXT    NOT NULL CHECK (action IN ('request', 'auto_add', 'notify')),
  schedule      TEXT,
  -- Off until previewed: run the job, show what it would add and why, change
  -- nothing (ADR-38's "preview before enabling").
  enabled       INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
  last_run_at   INTEGER
);
CREATE INDEX discovery_job_enabled ON discovery_job(enabled) WHERE enabled = 1;

CREATE TABLE quality_profile (
  id           INTEGER PRIMARY KEY,
  name         TEXT    NOT NULL UNIQUE,
  rules_json   TEXT    NOT NULL,   -- hard gates + compare tiers + additive score (ADR-40)
  upgrade_json TEXT    NOT NULL    -- min_delta, min_file_age, max_per_window, window
, suggested_rank INTEGER NOT NULL DEFAULT 100, media_type TEXT NOT NULL DEFAULT 'music');

CREATE TABLE language_profile (
  id                      INTEGER PRIMARY KEY,
  name                    TEXT    NOT NULL UNIQUE,
  audio_preferred_json    TEXT    NOT NULL,  -- ordered: ["ja","en"]
  audio_required_json     TEXT,              -- any_of; null/empty = no requirement
  subtitle_preferred_json TEXT,
  subtitle_required_json  TEXT,              -- any_of; null/empty = no requirement
  prefer_original_audio   INTEGER NOT NULL DEFAULT 0 CHECK (prefer_original_audio IN (0, 1)),
  reject_dubbed           INTEGER NOT NULL DEFAULT 0 CHECK (reject_dubbed IN (0, 1))
);

CREATE TABLE placement_affinity (
  category_id    INTEGER NOT NULL REFERENCES category(id),
  affinity_key   TEXT    NOT NULL,
  root_folder_id INTEGER NOT NULL REFERENCES root_folder(id),
  updated_at     INTEGER NOT NULL,
  PRIMARY KEY (category_id, affinity_key)
) WITHOUT ROWID;
CREATE INDEX placement_affinity_by_root ON placement_affinity(root_folder_id);

CREATE TABLE exclusion_pattern (
  id           INTEGER PRIMARY KEY,
  pattern      TEXT    NOT NULL,          -- glob against the file name, or *.ext
  action       TEXT    NOT NULL,          -- ignore | refuse
  media_type   TEXT,                      -- NULL = all; .iso is poison in music, fine in roms
  category_id  INTEGER,                   -- NULL = every category of that media type
  note         TEXT    NOT NULL DEFAULT '',  -- why this default exists; shown in the UI
  builtin      INTEGER NOT NULL DEFAULT 0,   -- shipped default; editable and removable
  enabled      INTEGER NOT NULL DEFAULT 1,
  UNIQUE(pattern, media_type, category_id)
);

CREATE TABLE import_list (
  id                 INTEGER PRIMARY KEY,
  category_id        INTEGER NOT NULL REFERENCES category(id) ON DELETE CASCADE,
  name               TEXT    NOT NULL,
  type               TEXT    NOT NULL,
  url                TEXT    NOT NULL DEFAULT '',
  cadence_hours      INTEGER NOT NULL DEFAULT 24,
  monitor            INTEGER NOT NULL DEFAULT 1 CHECK (monitor IN (0, 1)),
  quality_profile_id INTEGER REFERENCES quality_profile(id),
  -- Free text for now (e.g. "compilation, live, remix"), same shape
  -- SubscriptionFilters' own default exclusions already use -- nothing
  -- reads this yet, since there is no sync job to enforce it against.
  exclusions         TEXT    NOT NULL DEFAULT '',
  enabled            INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  UNIQUE (category_id, name)
);
CREATE INDEX import_list_by_category ON import_list(category_id);

CREATE TABLE download_folder (
  id               INTEGER PRIMARY KEY,
  path             TEXT    NOT NULL UNIQUE,
  label            TEXT,
  category_id      INTEGER REFERENCES category(id),
  -- Probed once, at creation, the same as root_folder's own writable
  -- column before the capacity job existed to refresh it -- nothing keeps
  -- this current yet, a named gap, not an oversight.
  readable         INTEGER NOT NULL DEFAULT 1 CHECK (readable IN (0, 1)),
  readable_reason  TEXT
, watch INTEGER NOT NULL DEFAULT 1 CHECK (watch IN (0, 1)), transfer_ladder TEXT);

CREATE TABLE naming_profile (
  id              INTEGER PRIMARY KEY,
  name            TEXT    NOT NULL UNIQUE,
  rename_files INTEGER NOT NULL DEFAULT 1, replace_illegal_characters INTEGER NOT NULL DEFAULT 1, colon_replacement TEXT NOT NULL DEFAULT 'smart');

CREATE TABLE indexer (
  id              INTEGER PRIMARY KEY,
  name            TEXT    NOT NULL UNIQUE,
  impl            TEXT    NOT NULL,      -- "newznab" | "torznab" (ADR-43); no CHECK here,
                                          -- the same open-ended shape arr_instance's own
                                          -- app column started before every value existed
  base_url        TEXT    NOT NULL,
  api_key         BLOB,
  enabled         INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  priority        INTEGER NOT NULL DEFAULT 50,
  -- Populated from the indexer's own t=caps response, never assumed (ADR-43).
  categories_json TEXT,
  caps_fetched_at INTEGER,
  -- egress = {proxy_url, flaresolverr_url} (ADR-47), expert tier -- unused until that
  -- pass wires it, present now so it needs no further migration when it does.
  settings_json   TEXT,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);

CREATE TABLE downloader (
  id           INTEGER PRIMARY KEY,
  name         TEXT    NOT NULL UNIQUE,
  impl         TEXT    NOT NULL,      -- "sabnzbd" today; more as later stages need them
  base_url     TEXT    NOT NULL,
  -- credentials is a single sealed secret (secrets.Cipher, the same shape indexer.api_key
  -- already uses) -- sufficient for SABnzbd's own single API key. A torrent client needing a
  -- username/password pair instead is this column's own later design pass, not assumed here.
  credentials  BLOB,
  enabled      INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  protocol     TEXT    NOT NULL,      -- "usenet" | "torrent" | "ddl" (ADR-46)
  settings_json TEXT,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);

CREATE TABLE naming_format (
  naming_profile_id INTEGER NOT NULL REFERENCES naming_profile(id) ON DELETE CASCADE,
  slot              TEXT    NOT NULL,
  template          TEXT    NOT NULL,
  PRIMARY KEY (naming_profile_id, slot)
) WITHOUT ROWID;

CREATE TABLE calendar_feed (
  user_id      INTEGER PRIMARY KEY REFERENCES app_user(id) ON DELETE CASCADE,
  token_hash   TEXT    NOT NULL UNIQUE,
  token_sealed BLOB    NOT NULL,
  created_at   INTEGER NOT NULL
);

CREATE TABLE category_quality_profile (
  category_id        INTEGER NOT NULL REFERENCES category(id) ON DELETE CASCADE,
  quality_profile_id INTEGER NOT NULL REFERENCES quality_profile(id),
  ord                INTEGER NOT NULL,
  PRIMARY KEY (category_id, quality_profile_id)
) WITHOUT ROWID;

CREATE TABLE category_file_management (
  category_id                 INTEGER PRIMARY KEY REFERENCES category(id) ON DELETE CASCADE,
  propers_repacks             TEXT    NOT NULL DEFAULT 'prefer_and_upgrade'
                              CHECK (propers_repacks IN ('prefer_and_upgrade', 'do_not_upgrade', 'do_not_prefer')),
  rescan_after_refresh        TEXT    NOT NULL DEFAULT 'always'
                              CHECK (rescan_after_refresh IN ('always', 'after_manual', 'never')),
  change_file_date            TEXT    NOT NULL DEFAULT 'none'
                              CHECK (change_file_date IN ('none', 'release_date')),
  recycle_retention_days      INTEGER NOT NULL DEFAULT 7 CHECK (recycle_retention_days BETWEEN 1 AND 365),
  delete_empty_folders        INTEGER NOT NULL DEFAULT 0 CHECK (delete_empty_folders IN (0, 1)),
  create_empty_artist_folders INTEGER NOT NULL DEFAULT 0 CHECK (create_empty_artist_folders IN (0, 1)),
  unmonitor_deleted           INTEGER NOT NULL DEFAULT 0 CHECK (unmonitor_deleted IN (0, 1)),
  set_permissions             INTEGER NOT NULL DEFAULT 0 CHECK (set_permissions IN (0, 1)),
  chmod_folder                TEXT    NOT NULL DEFAULT '755',
  chown_group                 TEXT    NOT NULL DEFAULT ''
, write_ids_to_tags INTEGER NOT NULL DEFAULT 1
  CHECK (write_ids_to_tags IN (0, 1)), replace_ids_in_tags INTEGER NOT NULL DEFAULT 0
  CHECK (replace_ids_in_tags IN (0, 1)), write_metadata_to_tags INTEGER NOT NULL DEFAULT 0
  CHECK (write_metadata_to_tags IN (0, 1)), embed_cover_in_tags INTEGER NOT NULL DEFAULT 0
  CHECK (embed_cover_in_tags IN (0, 1)), merge_artist_aliases INTEGER NOT NULL DEFAULT 0 CHECK (merge_artist_aliases IN (0, 1)), navidrome_profile INTEGER NOT NULL DEFAULT 0 CHECK (navidrome_profile IN (0, 1)), import_extra_files INTEGER CHECK (import_extra_files IN (0, 1)), extra_file_extensions TEXT, media_server_files INTEGER NOT NULL DEFAULT 0 CHECK (media_server_files IN (0, 1)), shared_file_fallback TEXT NOT NULL DEFAULT 'copy' CHECK (shared_file_fallback IN ('copy', 'symlink')), ignore_new_seasons INTEGER NOT NULL DEFAULT 0 CHECK (ignore_new_seasons IN (0, 1)));

CREATE TABLE "arr_instance" (
  id               INTEGER PRIMARY KEY,
  app              TEXT    NOT NULL CHECK (app IN ('lidarr', 'sonarr', 'radarr', 'readarr', 'whisparr', 'prowlarr')),
  name             TEXT    NOT NULL,
  base_url         TEXT    NOT NULL,
  api_key          BLOB    NOT NULL,
  category_id      INTEGER REFERENCES category(id),
  enabled          INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL,
  last_synced_at   INTEGER,
  last_sync_error  TEXT,
  UNIQUE (app, name)
);

CREATE TABLE quality_target (
  id                 INTEGER PRIMARY KEY,
  name               TEXT    NOT NULL,
  media_type         TEXT    NOT NULL,
  audio_codec        TEXT    NOT NULL DEFAULT '',
  container          TEXT    NOT NULL DEFAULT '',
  bitrate_kbps       INTEGER,
  tolerance_pct      INTEGER NOT NULL DEFAULT 10,
  resolution         TEXT    NOT NULL DEFAULT '',
  video_codec        TEXT    NOT NULL DEFAULT '',
  hdr                TEXT    NOT NULL DEFAULT '',
  audio_channels     TEXT    NOT NULL DEFAULT '',
  audio_languages    TEXT    NOT NULL DEFAULT '',
  subtitle_languages TEXT    NOT NULL DEFAULT '',
  audio_bitrate_kbps INTEGER,
  video_bitrate_kbps INTEGER,
  frame_rate         REAL,
  crf                REAL,
  UNIQUE (media_type, name)
);

CREATE TABLE category_target (
  category_id INTEGER PRIMARY KEY REFERENCES category(id) ON DELETE CASCADE,
  target_id   INTEGER NOT NULL REFERENCES quality_target(id)
);

CREATE TABLE category_metadata_source (
  category_id INTEGER NOT NULL REFERENCES category(id) ON DELETE CASCADE,
  source      TEXT    NOT NULL,
  ord         INTEGER NOT NULL,
  PRIMARY KEY (category_id, source)
) WITHOUT ROWID;

INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (1, 'Lossless preferred', '{"quality_profile":"Lossless preferred","compare":["lossless","sample_rate","bit_depth","bitrate_distance","audio_codec","score"],"tiers":{"audio_codec":[["flac","alac"],["ogg","opus"],["aac"],["mp3"]]},"targets":{"bitrate":[{"when":{"audio_codec":"mp3"},"min":"192kbps","ideal":"320kbps","max":"400kbps"},{"when":{"audio_codec":"aac"},"min":"160kbps","ideal":"256kbps","max":"320kbps"}]}}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 3, 'music');
INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (2, 'Lossy 320', '{"quality_profile":"Lossy 320","compare":["audio_codec","bitrate_distance","score"],"tiers":{"audio_codec":[["mp3","aac","ogg","opus"],["flac","alac"]]},"targets":{"bitrate":[{"when":{"audio_codec":"mp3"},"min":"256kbps","ideal":"320kbps","max":"320kbps"},{"when":{"audio_codec":"aac"},"min":"224kbps","ideal":"256kbps","max":"320kbps"},{"when":{"audio_codec":"opus"},"min":"160kbps","ideal":"192kbps","max":"256kbps"}]}}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 2, 'music');
INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (3, 'Space conscious', '{"quality_profile":"Space conscious","compare":["audio_codec","bitrate_distance","score"],"tiers":{"audio_codec":[["opus"],["aac"],["ogg"],["mp3"],["flac","alac"]]},"targets":{"bitrate":[{"when":{"audio_codec":"opus"},"min":"96kbps","ideal":"128kbps","max":"160kbps"},{"when":{"audio_codec":"aac"},"min":"128kbps","ideal":"160kbps","max":"192kbps"},{"when":{"audio_codec":"mp3"},"min":"160kbps","ideal":"192kbps","max":"224kbps"}]}}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 1, 'music');
INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (4, 'WEB 1080p', '{"quality_profile":"WEB 1080p","require":{"resolution":{"min":"720p","max":"1080p"},"reject_codecs":["xvid","divx"]},"compare":["resolution","source","video_codec","audio_channels","audio_codec","score"],"tiers":{"resolution":[["1080p","1080i"],["720p"],["576p","480p","360p"],["2160p"]],"source":[["webdl"],["webrip"],["bluray","remux"],["hdtv"]],"video_codec":[["h264"],["h265","av1"]],"audio_channels":[["5.1"],["2.0"]],"audio_codec":[["eac3","ac3"],["aac"]]}}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 100, 'tv');
INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (5, 'HD 720p/1080p', '{"quality_profile":"HD 720p/1080p","require":{"resolution":{"min":"720p","max":"1080p"},"reject_codecs":["xvid","divx"]},"compare":["resolution","source","video_codec","audio_channels","score"],"tiers":{"resolution":[["1080p","1080i"],["720p"],["576p","480p","360p"],["2160p"]],"source":[["bluray","remux"],["webdl"],["webrip"],["hdtv"]],"video_codec":[["h265","av1"],["h264"]],"audio_channels":[["5.1"],["2.0"]]}}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 100, 'tv');
INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (6, 'UHD 2160p', '{"quality_profile":"UHD 2160p","require":{"resolution":{"min":"1080p","max":"2160p"},"reject_codecs":["xvid","divx"]},"compare":["resolution","source","video_codec","hdr","audio_channels","audio_codec","score"],"tiers":{"resolution":[["2160p"],["1080p","1080i"],["720p"],["576p","480p","360p"]],"source":[["remux"],["bluray"],["webdl"],["webrip"],["hdtv"]],"video_codec":[["h265","av1"],["h264"]],"hdr":[["dolby-vision"],["hdr10plus","hdr10"],["hdr","hlg"]],"audio_channels":[["7.1"],["5.1"],["2.0"]],"audio_codec":[["truehd","dtshd"],["eac3","dts"],["ac3","aac"]]}}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 100, 'tv');
INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (7, 'Space conscious (x265 1080p)', '{"quality_profile":"Space conscious (x265 1080p)","require":{"resolution":{"min":"720p","max":"1080p"},"reject_codecs":["xvid","divx"]},"compare":["video_codec","resolution","bitrate_distance","source","score"],"tiers":{"video_codec":[["h265","av1"],["h264"]],"resolution":[["1080p","1080i"],["720p"],["576p","480p","360p"],["2160p"]],"source":[["webdl","bluray"],["webrip"],["hdtv"]]},"targets":{"bitrate":[{"when":{"video_codec":"h265","resolution":"1080p"},"min":"1Mbps","ideal":"3Mbps","max":"6Mbps"},{"when":{"video_codec":"h264","resolution":"1080p"},"min":"2Mbps","ideal":"5Mbps","max":"9Mbps"}]}}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 100, 'tv');
INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (8, 'Anime 1080p', '{"quality_profile":"Anime 1080p","require":{"resolution":{"min":"720p","max":"1080p"},"audio_language":{"any_of":["ja","en"]}},"compare":["resolution","source","video_codec","score"],"tiers":{"resolution":[["1080p","1080i"],["720p"],["576p","480p","360p"],["2160p"]],"source":[["bluray","remux"],["webdl"],["webrip"],["hdtv"]],"video_codec":[["h265","av1"],["h264"]]}}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 100, 'tv');
INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (9, 'SD', '{"quality_profile":"SD","require":{"resolution":{"min":"480p","max":"576p"}},"compare":["resolution","source","score"],"tiers":{"resolution":[["576p","480p"],["360p"],["720p"],["1080p","1080i"],["2160p"]],"source":[["dvd"],["webdl","webrip"],["hdtv"]]}}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 100, 'tv');
INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (10, 'Any', '{"quality_profile":"Any","compare":["resolution","source","score"],"tiers":{"resolution":[["2160p"],["1080p","1080i"],["720p"],["576p","480p"]],"source":[["remux"],["bluray"],["webdl"],["webrip"],["hdtv"],["dvd"]]},"scoring":[{"name":"Starved (under 0.4 Mbps)","score":-15,"specs":[{"field":"video_bitrate","lt":400000,"required":true}]},{"name":"Bloated (over 25 Mbps)","score":-15,"specs":[{"field":"video_bitrate","gt":25000000,"required":true}]}]}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 100, 'tv');
INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (11, 'Movie HD 1080p', '{"quality_profile":"Movie HD 1080p","require":{"resolution":{"min":"720p","max":"1080p"},"reject_codecs":["xvid","divx"]},"compare":["resolution","source","video_codec","audio_channels","audio_codec","score"],"tiers":{"resolution":[["1080p","1080i"],["720p"],["576p","480p","360p"],["2160p"]],"source":[["bluray","remux"],["webdl"],["webrip"],["hdtv"]],"video_codec":[["h264"],["h265","av1"]],"audio_channels":[["5.1"],["2.0"]],"audio_codec":[["eac3","ac3","dts"],["aac"]]}}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 100, 'movie');
INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (12, 'Movie UHD 2160p', '{"quality_profile":"Movie UHD 2160p","require":{"resolution":{"min":"1080p","max":"2160p"},"reject_codecs":["xvid","divx"]},"compare":["resolution","source","video_codec","hdr","audio_channels","audio_codec","score"],"tiers":{"resolution":[["2160p"],["1080p","1080i"],["720p"],["576p","480p","360p"]],"source":[["remux"],["bluray"],["webdl"],["webrip"],["hdtv"]],"video_codec":[["h265","av1"],["h264"]],"hdr":[["dolby-vision"],["hdr10plus","hdr10"],["hdr","hlg"]],"audio_channels":[["7.1"],["5.1"],["2.0"]],"audio_codec":[["truehd","dtshd"],["eac3","dts"],["ac3","aac"]]}}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 100, 'movie');
INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (13, 'Movie space conscious (x265 1080p)', '{"quality_profile":"Movie space conscious (x265 1080p)","require":{"resolution":{"min":"720p","max":"1080p"},"reject_codecs":["xvid","divx"]},"compare":["video_codec","resolution","bitrate_distance","source","score"],"tiers":{"video_codec":[["h265","av1"],["h264"]],"resolution":[["1080p","1080i"],["720p"],["576p","480p","360p"],["2160p"]],"source":[["webdl","bluray"],["webrip"],["hdtv"]]},"targets":{"bitrate":[{"when":{"video_codec":"h265","resolution":"1080p"},"min":"1.5Mbps","ideal":"4Mbps","max":"8Mbps"},{"when":{"video_codec":"h264","resolution":"1080p"},"min":"3Mbps","ideal":"7Mbps","max":"12Mbps"}]}}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 100, 'movie');
INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (14, 'Movie SD', '{"quality_profile":"Movie SD","require":{"resolution":{"min":"480p","max":"576p"}},"compare":["resolution","source","score"],"tiers":{"resolution":[["576p","480p"],["360p"],["720p"],["1080p","1080i"],["2160p"]],"source":[["dvd"],["webdl","webrip"],["hdtv"]]}}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 100, 'movie');
INSERT INTO quality_profile (id, name, rules_json, upgrade_json, suggested_rank, media_type) VALUES (15, 'Movie Any', '{"quality_profile":"Movie Any","compare":["resolution","source","score"],"tiers":{"resolution":[["2160p"],["1080p","1080i"],["720p"],["576p","480p"]],"source":[["remux"],["bluray"],["webdl"],["webrip"],["hdtv"],["dvd"]]},"scoring":[{"name":"Starved (under 1 Mbps)","score":-15,"specs":[{"field":"video_bitrate","lt":1000000,"required":true}]},{"name":"Bloated (over 60 Mbps)","score":-15,"specs":[{"field":"video_bitrate","gt":60000000,"required":true}]}]}', '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}', 100, 'movie');
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (1, '*.sfv', 'ignore', NULL, NULL, 'Checksum sidecar, not media', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (2, '*.srr', 'ignore', NULL, NULL, 'ReScene recovery data, not media', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (3, '*.par2', 'ignore', NULL, NULL, 'Parity recovery data, not media', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (4, '*.nfo', 'ignore', NULL, NULL, 'Release info text, not media', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (5, '*.txt', 'ignore', NULL, NULL, 'Plain text, not media', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (6, '*.url', 'ignore', NULL, NULL, 'Shortcut file, not media', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (7, 'Thumbs.db', 'ignore', NULL, NULL, 'Windows thumbnail cache', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (8, '.DS_Store', 'ignore', NULL, NULL, 'macOS folder metadata', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (9, 'sample.*', 'ignore', NULL, NULL, 'A promotional sample, not the release itself', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (10, '*.exe', 'refuse', NULL, NULL, 'An executable beside media is the classic shape of a fake', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (11, '*.scr', 'refuse', NULL, NULL, 'A screensaver executable is still an executable', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (12, '*.bat', 'refuse', NULL, NULL, 'A batch script beside media is a red flag, not clutter', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (13, '*.cmd', 'refuse', NULL, NULL, 'A command script beside media is a red flag, not clutter', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (14, '*.com', 'refuse', NULL, NULL, 'A legacy DOS executable is still an executable', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (15, '*.msi', 'refuse', NULL, NULL, 'An installer beside media is a red flag, not clutter', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (16, '*.vbs', 'refuse', NULL, NULL, 'A VBScript beside media is a red flag, not clutter', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (17, '*.lnk', 'refuse', NULL, NULL, 'A Windows shortcut can point at an executable payload', 1, 1);
INSERT INTO exclusion_pattern (id, pattern, action, media_type, category_id, note, builtin, enabled) VALUES (18, '*.iso', 'refuse', 'music', NULL, 'A disc image is poison in a music release; legitimate in ROMs', 1, 1);
