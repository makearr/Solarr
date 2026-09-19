-- Which release types a monitored artist brings in. The user's own answer, 2026-09-19:
-- "Albums EP Singles by default. this default can be set globally for the library and be
-- overriden by the user per artist". This column is the library half; the per-artist half
-- is the subscription's own filters_json, which already overrides what it names.
-- A JSON array, matched against MusicBrainz's own primary-type vocabulary.
ALTER TABLE category ADD COLUMN monitor_release_types TEXT NOT NULL
  DEFAULT '["Album","EP","Single"]';
