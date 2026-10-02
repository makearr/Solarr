-- Physical files. Column lists follow table order, or sqlc stops reusing models.

-- name: InternAttributes :one
INSERT INTO media_attributes (attr_hash, resolution, source, video_codec, hdr,
                              audio_codec, audio_channels, bit_depth, languages,
                              subtitles, edition_tokens, release_group, proper_repack,
                              frame_rate, scan_type, container, lossless, sample_rate,
                              image_format, colour_depth, page_count, bitrate_kbps,
                              bitrate_mode, vbr_preset, lyrics_languages)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (attr_hash) DO UPDATE SET attr_hash = excluded.attr_hash
RETURNING id, attr_hash, resolution, source, video_codec, hdr, audio_codec,
          audio_channels, bit_depth, languages, subtitles, edition_tokens,
          release_group, proper_repack, frame_rate, scan_type, container,
          lossless, sample_rate, image_format, colour_depth, page_count,
          bitrate_kbps, bitrate_mode, vbr_preset, lyrics_languages;

-- name: GetAttributes :one
SELECT id, attr_hash, resolution, source, video_codec, hdr, audio_codec,
       audio_channels, bit_depth, languages, subtitles, edition_tokens,
       release_group, proper_repack, frame_rate, scan_type, container,
       lossless, sample_rate, image_format, colour_depth, page_count,
       bitrate_kbps, bitrate_mode, vbr_preset, lyrics_languages
FROM media_attributes WHERE id = ?;

-- Returning the row costs four times the insert itself at import scale, so this is
-- execlastid: documentation/benchmarks.md.
-- name: CreateMediaObject :execlastid
INSERT INTO media_object (size_bytes, quick_hash, content_hash, oshash, phash,
                          audio_fp, audio_fp_prefix, fingerprinted_at,
                          claimed_attributes_id, measured_attributes_id,
                          attribute_mismatch_json, duration_ms, width, height,
                          video_bitrate, audio_bitrate, overall_bitrate,
                          probe_json, first_seen_at, verified_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetMediaObject :one
SELECT id, size_bytes, quick_hash, content_hash, oshash, phash, audio_fp,
       audio_fp_prefix, fingerprinted_at, claimed_attributes_id,
       measured_attributes_id, attribute_mismatch_json, duration_ms, width,
       height, video_bitrate, audio_bitrate, overall_bitrate, probe_json,
       first_seen_at, verified_at
FROM media_object WHERE id = ?;

-- The cheap identity check an incremental scan runs before reading a file.
-- name: FindMediaObjectsByQuickHash :many
SELECT id, size_bytes, quick_hash, content_hash, oshash, phash, audio_fp,
       audio_fp_prefix, fingerprinted_at, claimed_attributes_id,
       measured_attributes_id, attribute_mismatch_json, duration_ms, width,
       height, video_bitrate, audio_bitrate, overall_bitrate, probe_json,
       first_seen_at, verified_at
FROM media_object WHERE quick_hash = ?;

-- oshash is exact identity: the same bytes, with no decoding (ADR-35).
-- name: GetMediaObjectByOshash :one
SELECT id, size_bytes, quick_hash, content_hash, oshash, phash, audio_fp,
       audio_fp_prefix, fingerprinted_at, claimed_attributes_id,
       measured_attributes_id, attribute_mismatch_json, duration_ms, width,
       height, video_bitrate, audio_bitrate, overall_bitrate, probe_json,
       first_seen_at, verified_at
FROM media_object WHERE oshash = ?;

-- Measured attributes replace claimed ones after probing; the claim is kept so a
-- mismatch stays visible (ADR-39). The scalars land on the object, not the
-- interned row, because they are unique per file.
-- name: SetMeasuredAttributes :exec
UPDATE media_object
SET measured_attributes_id = ?, attribute_mismatch_json = ?,
    duration_ms = ?, width = ?, height = ?,
    video_bitrate = ?, audio_bitrate = ?, overall_bitrate = ?,
    probe_json = ?, verified_at = ?
WHERE id = ?;

-- The claimed half (todo 4.2): derived once per release folder at scan time,
-- from the release name rather than the file's own bytes, so it needs no
-- scalar columns or mismatch comparison of its own (RecordMeasured's own
-- namesakes) -- those only mean something once a measured claim exists to
-- compare against.
-- name: SetClaimedAttributes :exec
UPDATE media_object SET claimed_attributes_id = ? WHERE id = ?;

-- Fingerprint generation is the lowest-priority background work and resumable,
-- so it asks for the next unfingerprinted rows rather than scanning (ADR-35).
-- name: ListUnfingerprinted :many
SELECT id, size_bytes, quick_hash, content_hash, oshash, phash, audio_fp,
       audio_fp_prefix, fingerprinted_at, claimed_attributes_id,
       measured_attributes_id, attribute_mismatch_json, duration_ms, width,
       height, video_bitrate, audio_bitrate, overall_bitrate, probe_json,
       first_seen_at, verified_at
FROM media_object
WHERE fingerprinted_at IS NULL AND id > sqlc.arg(after_id)
ORDER BY id
LIMIT sqlc.arg(page_size);

-- name: SetFingerprints :exec
UPDATE media_object
SET oshash = ?, phash = ?, audio_fp = ?, audio_fp_prefix = ?, fingerprinted_at = ?
WHERE id = ?;

-- The whole phash set is 4 MB at 500k files: Hamming distance is brute-forced in
-- Go, so this hands over every candidate rather than filtering in SQL (ADR-35).
-- name: ListPerceptualHashes :many
SELECT id, phash FROM media_object WHERE phash IS NOT NULL ORDER BY id;

-- The measured half of ADR-39 is the lowest-priority background work, resumable
-- the same way fingerprinting is (todo 4.2): a cursor on media_object.id rather
-- than re-deriving "unmeasured" from scratch, so a file that fails its probe
-- this run does not block the cursor from ever reaching what comes after it.
-- The join to library_path is within library.db, not across databases --
-- both tables live here, so it does not run into the no-cross-DB-join rule.
-- name: ListUnmeasured :many
SELECT o.id, o.size_bytes, lp.root_folder_id, lp.rel_path
FROM media_object o
JOIN library_path lp ON lp.media_object_id = o.id AND lp.is_primary = 1
WHERE o.measured_attributes_id IS NULL AND o.id > sqlc.arg(after_id)
ORDER BY o.id
LIMIT sqlc.arg(page_size);

-- Probe coverage, the same per-root shape CountLibraryPathsByRootFolder already
-- answers for the denominator (library.CategoryCounters' own precedent) --
-- "probed" is the numerator a coverage counter reconciles against it.
-- name: CountMeasuredByRootFolder :many
SELECT lp.root_folder_id, CAST(count(*) AS INTEGER) AS total
FROM library_path lp
JOIN media_object o ON o.id = lp.media_object_id
WHERE o.measured_attributes_id IS NOT NULL
GROUP BY lp.root_folder_id
ORDER BY lp.root_folder_id;

-- The second half of importing a file, and execlastid for the same reason.
-- name: AddLibraryPath :execlastid
INSERT INTO library_path (media_object_id, root_folder_id, rel_path, link_kind,
                          is_primary, created_at, last_seen_at, missing_since)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListPathsForObject :many
SELECT id, media_object_id, root_folder_id, rel_path, link_kind, is_primary,
       created_at, last_seen_at, missing_since
FROM library_path WHERE media_object_id = ? ORDER BY is_primary DESC, id;

-- name: GetPathByLocation :one
SELECT id, media_object_id, root_folder_id, rel_path, link_kind, is_primary,
       created_at, last_seen_at, missing_since
FROM library_path WHERE root_folder_id = ? AND rel_path = ?;

-- name: DeleteLibraryPath :exec
DELETE FROM library_path WHERE id = ?;

-- A vanished path is MISSING, not deleted, until the root is confirmed online
-- (section 10.4), so the scan lists a root rather than removing from it.
-- name: ListPathsInRoot :many
SELECT id, media_object_id, root_folder_id, rel_path, link_kind, is_primary,
       created_at, last_seen_at, missing_since
FROM library_path
WHERE root_folder_id = sqlc.arg(root_folder_id) AND id > sqlc.arg(after_id)
ORDER BY id
LIMIT sqlc.arg(page_size);

-- ADR-51's drain: every real file to evacuate, with its size for
-- mover.PlanDrain's own affinity-group byte totals. Primary paths only,
-- the same convention ListUnmeasured already uses -- a secondary,
-- cross-entry-hardlinked path for the same object (ADR-11) sharing this
-- root is a known, narrow gap move_item's own (move_job_id, media_object_id)
-- primary key already forces (one item per object per job), not something
-- this query can widen on its own.
-- name: ListPathsForRootWithSize :many
SELECT lp.media_object_id, lp.rel_path, o.size_bytes
FROM library_path lp
JOIN media_object o ON o.id = lp.media_object_id
WHERE lp.root_folder_id = sqlc.arg(root_folder_id) AND lp.is_primary = 1
  AND lp.media_object_id > sqlc.arg(after_id)
ORDER BY lp.media_object_id
LIMIT sqlc.arg(page_size);

-- Re-observing a known path proves it is still present, and clears any earlier
-- missing mark (section 10.4): a file coming back is exactly the case this exists
-- for.
-- name: TouchLibraryPath :exec
UPDATE library_path SET last_seen_at = ?, missing_since = NULL WHERE id = ?;

-- ADR-51's own RECORD step: media_object never changes (Invariant 70), only
-- where it lives. Matched by the old (root_folder_id, rel_path) rather than
-- by id, since that pair is what Apply already has in hand from its own
-- PlannedMove; :execrows lets the caller confirm exactly one row moved
-- rather than trusting a silent no-op.
-- name: RelocateLibraryPath :execrows
UPDATE library_path SET root_folder_id = sqlc.arg(new_root_folder_id), rel_path = sqlc.arg(new_rel_path)
WHERE root_folder_id = sqlc.arg(old_root_folder_id) AND rel_path = sqlc.arg(old_rel_path);

-- Bounded per Invariant 2: the caller loops this until a pass affects zero rows,
-- never one UPDATE over the whole root.
-- name: MarkLibraryPathsMissing :execrows
UPDATE library_path SET missing_since = ?
WHERE id IN (
  SELECT p.id FROM library_path p
  WHERE p.root_folder_id = ? AND p.last_seen_at < ? AND p.missing_since IS NULL
  LIMIT ?
);

-- name: DeleteMediaObject :exec
DELETE FROM media_object WHERE id = ?;

-- The root_folder_id half of the ADR-25 orphan check. See CountDesiredItemsByCategory.
-- name: CountLibraryPathsByRootFolder :many
SELECT root_folder_id, CAST(count(*) AS INTEGER) AS total
FROM library_path GROUP BY root_folder_id ORDER BY root_folder_id;
