-- What the probe read of each file leaves media_object, the hottest table, for its own (revision 2, D1): a JSON blob
-- per file widened every row read, and stale probes were found by LIKE over every blob. probe_version says what a
-- probe read: 1 before the lyrics language, 2 before the subtitles and the encoder's CRF, 3 all of them. The old
-- column stays until a later migration drops it; nothing writes it any more.
CREATE TABLE media_probe (
  media_object_id INTEGER PRIMARY KEY REFERENCES media_object(id) ON DELETE CASCADE,
  probe_version   INTEGER NOT NULL,
  probed_at       INTEGER NOT NULL,
  json            TEXT    NOT NULL
);
CREATE INDEX media_probe_version ON media_probe(probe_version, media_object_id);
INSERT INTO media_probe (media_object_id, probe_version, probed_at, json)
SELECT id,
       CASE WHEN probe_json NOT LIKE '%"LyricsLanguages"%' THEN 1
            WHEN probe_json NOT LIKE '%"SubtitleLanguages"%' OR probe_json NOT LIKE '%"CRF"%' THEN 2
            ELSE 3 END,
       COALESCE(verified_at, first_seen_at), probe_json
FROM media_object
WHERE probe_json IS NOT NULL;
