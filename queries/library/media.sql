-- Physical files. Column lists follow table order, or sqlc stops reusing models.

-- name: InternAttributes :one
INSERT INTO media_attributes (attr_hash, resolution, source, video_codec, hdr,
                              audio_codec, audio_channels, bit_depth, languages,
                              subtitles, edition_tokens, release_group, proper_repack,
                              frame_rate, scan_type, container, lossless, sample_rate,
                              image_format, colour_depth, page_count, bitrate_kbps,
                              bitrate_mode, vbr_preset, lyrics_languages, crf)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (attr_hash) DO UPDATE SET attr_hash = excluded.attr_hash
RETURNING id, attr_hash, resolution, source, video_codec, hdr, audio_codec,
          audio_channels, bit_depth, languages, subtitles, edition_tokens,
          release_group, proper_repack, frame_rate, scan_type, container,
          lossless, sample_rate, image_format, colour_depth, page_count,
          bitrate_kbps, bitrate_mode, vbr_preset, lyrics_languages, crf;

-- name: GetAttributes :one
SELECT id, attr_hash, resolution, source, video_codec, hdr, audio_codec,
       audio_channels, bit_depth, languages, subtitles, edition_tokens,
       release_group, proper_repack, frame_rate, scan_type, container,
       lossless, sample_rate, image_format, colour_depth, page_count,
       bitrate_kbps, bitrate_mode, vbr_preset, lyrics_languages, crf
FROM media_attributes WHERE id = ?;

-- Returning the row costs four times the insert itself at import scale, so this is
-- execlastid: documentation/benchmarks.md.
-- name: CreateMediaObject :execlastid
INSERT INTO media_object (size_bytes, quick_hash, content_hash, oshash, phash,
                          audio_fp, audio_fp_prefix, fingerprinted_at,
                          claimed_attributes_id, measured_attributes_id,
                          attribute_mismatch_json, duration_ms, width, height,
                          video_bitrate, audio_bitrate, overall_bitrate,
                          probe_json, first_seen_at, verified_at, kind)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetMediaObject :one
SELECT id, size_bytes, quick_hash, content_hash, oshash, phash, audio_fp,
       audio_fp_prefix, fingerprinted_at, claimed_attributes_id,
       measured_attributes_id, attribute_mismatch_json, duration_ms, width,
       height, video_bitrate, audio_bitrate, overall_bitrate, probe_json,
       first_seen_at, verified_at, kind
FROM media_object WHERE id = ?;

-- The cheap identity check an incremental scan runs before reading a file.
-- name: FindMediaObjectsByQuickHash :many
SELECT id, size_bytes, quick_hash, content_hash, oshash, phash, audio_fp,
       audio_fp_prefix, fingerprinted_at, claimed_attributes_id,
       measured_attributes_id, attribute_mismatch_json, duration_ms, width,
       height, video_bitrate, audio_bitrate, overall_bitrate, probe_json,
       first_seen_at, verified_at, kind
FROM media_object WHERE quick_hash = ?;

-- oshash is exact identity: the same bytes, with no decoding (ADR-35).
-- name: GetMediaObjectByOshash :one
SELECT id, size_bytes, quick_hash, content_hash, oshash, phash, audio_fp,
       audio_fp_prefix, fingerprinted_at, claimed_attributes_id,
       measured_attributes_id, attribute_mismatch_json, duration_ms, width,
       height, video_bitrate, audio_bitrate, overall_bitrate, probe_json,
       first_seen_at, verified_at, kind
FROM media_object WHERE oshash = ?;

-- Measured attributes replace claimed ones after probing; the claim is kept so a
-- mismatch stays visible (ADR-39). The scalars land on the object, not the
-- interned row, because they are unique per file.
-- name: SetMeasuredAttributes :exec
UPDATE media_object
SET measured_attributes_id = ?, attribute_mismatch_json = ?,
    duration_ms = ?, width = ?, height = ?,
    video_bitrate = ?, audio_bitrate = ?, overall_bitrate = ?,
    probe_json = NULL, verified_at = ?
WHERE id = ?;

-- What the probe read of one file, replacing an earlier probe.
-- name: SetMediaProbe :exec
INSERT INTO media_probe (media_object_id, probe_version, probed_at, json) VALUES (?, ?, ?, ?)
ON CONFLICT (media_object_id) DO UPDATE SET
  probe_version = excluded.probe_version, probed_at = excluded.probed_at, json = excluded.json;

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
       first_seen_at, verified_at, kind
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

-- Files whose probe predates what the probe now reads of every file (below_version), read once more.
-- name: ListStaleProbes :many
SELECT o.id, o.size_bytes, lp.root_folder_id, lp.rel_path
FROM media_probe mp
JOIN media_object o ON o.id = mp.media_object_id
JOIN library_path lp ON lp.media_object_id = o.id AND lp.is_primary = 1
WHERE mp.probe_version < sqlc.arg(below_version) AND o.measured_attributes_id IS NOT NULL AND o.id > sqlc.arg(after_id)
ORDER BY o.id
LIMIT sqlc.arg(page_size);

-- Video files whose probe predates what the probe now reads of a video (below_version): the picture, the subtitle
-- tracks and the encoder's CRF.
-- name: ListStaleVideoProbes :many
SELECT o.id, o.size_bytes, lp.root_folder_id, lp.rel_path
FROM media_probe mp
JOIN media_object o ON o.id = mp.media_object_id
JOIN library_path lp ON lp.media_object_id = o.id AND lp.is_primary = 1
WHERE mp.probe_version < sqlc.arg(below_version) AND o.measured_attributes_id IS NOT NULL AND o.id > sqlc.arg(after_id)
  AND o.kind = 'video'
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

-- One work's files on one root, each by its primary path: a rename scoped to one work.
-- name: ListWorkPathsOnRoot :many
SELECT lp.media_object_id, lp.rel_path, o.size_bytes
FROM work_file f
JOIN library_path lp ON lp.media_object_id = f.media_object_id AND lp.is_primary = 1
JOIN media_object o ON o.id = f.media_object_id
WHERE f.work_id = sqlc.arg(work_id) AND lp.root_folder_id = sqlc.arg(root_folder_id)
ORDER BY lp.media_object_id;

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
-- The file's own row marked missing where it moves back to (a folder renamed by hand, then rescanned) is stale.
-- name: DeleteStaleDestinationPath :exec
DELETE FROM library_path
WHERE library_path.root_folder_id = sqlc.arg(new_root_folder_id) AND library_path.rel_path = sqlc.arg(new_rel_path)
  AND library_path.missing_since IS NOT NULL
  AND library_path.media_object_id = (SELECT o.media_object_id FROM library_path o
                         WHERE o.root_folder_id = sqlc.arg(old_root_folder_id) AND o.rel_path = sqlc.arg(old_rel_path));

-- name: RelocateLibraryPath :execrows
UPDATE library_path SET root_folder_id = sqlc.arg(new_root_folder_id), rel_path = sqlc.arg(new_rel_path)
WHERE root_folder_id = sqlc.arg(old_root_folder_id) AND rel_path = sqlc.arg(old_rel_path);

-- What a full sweep would mark missing, against everything the root holds, before it marks anything.
-- name: CountVanishingPaths :one
SELECT CAST(COALESCE(SUM(CASE WHEN missing_since IS NULL AND last_seen_at < sqlc.arg(before) THEN 1 ELSE 0 END), 0) AS INTEGER) AS vanishing,
       CAST(COUNT(*) AS INTEGER) AS total
FROM library_path WHERE root_folder_id = sqlc.arg(root_folder_id);

-- The works holding a file one sweep found gone, so their counts can follow.
-- name: ListWorksWithPathsMissingSince :many
SELECT DISTINCT di.work_id
FROM library_path p
JOIN desired_item_file f ON f.media_object_id = p.media_object_id
JOIN desired_item di ON di.id = f.desired_item_id
WHERE p.root_folder_id = sqlc.arg(root_folder_id) AND p.missing_since = sqlc.arg(missing_since)
  AND di.work_id > sqlc.arg(after_id)
ORDER BY di.work_id
LIMIT sqlc.arg(page_size);

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

-- Media objects not yet given a kind, oldest first, each with its primary path: what the start-up pass classifies.
-- name: ListUnclassifiedMediaObjects :many
SELECT o.id, CAST(COALESCE((SELECT lp.rel_path FROM library_path lp WHERE lp.media_object_id = o.id
                            ORDER BY lp.is_primary DESC, lp.id LIMIT 1), '') AS TEXT) AS rel_path
FROM media_object o
WHERE o.kind IS NULL AND o.id > sqlc.arg(after_id)
ORDER BY o.id
LIMIT sqlc.arg(page_size);

-- name: SetMediaObjectKind :exec
UPDATE media_object SET kind = ? WHERE id = ?;

-- The works a file counts towards.
-- name: ListWorkIDsOfObject :many
SELECT DISTINCT di.work_id FROM desired_item_file f JOIN desired_item di ON di.id = f.desired_item_id
WHERE f.media_object_id = ? ORDER BY di.work_id;

-- A rename's files: each primary file on a root still there, with every part of one library it covers, a page of
-- files at a time (all of one file's rows on one page); work_id narrows to one work, 0 is every work.
-- name: ListRenameFilesOnRoot :many
SELECT lp.media_object_id, lp.rel_path, o.size_bytes, d.work_id, CAST(COALESCE(d.edition_id, 0) AS INTEGER) AS edition_id,
       f.media_ref
FROM library_path lp
JOIN media_object o ON o.id = lp.media_object_id
JOIN desired_item_file f ON f.media_object_id = lp.media_object_id AND f.role = 'primary'
JOIN desired_item d ON d.id = f.desired_item_id AND d.category_id = sqlc.arg(category_id)
WHERE lp.media_object_id IN (
    SELECT p.media_object_id FROM library_path p
    WHERE p.root_folder_id = sqlc.arg(root_folder_id) AND p.is_primary = 1 AND p.missing_since IS NULL
      AND p.media_object_id > sqlc.arg(after_id)
      AND (CAST(sqlc.arg(work_id) AS INTEGER) = 0 OR EXISTS (
        SELECT 1 FROM desired_item_file wf JOIN desired_item wd ON wd.id = wf.desired_item_id
        WHERE wf.media_object_id = p.media_object_id AND wd.work_id = sqlc.arg(work_id)))
    ORDER BY p.media_object_id
    LIMIT sqlc.arg(page_size))
  AND lp.root_folder_id = sqlc.arg(root_folder_id) AND lp.is_primary = 1 AND lp.missing_since IS NULL
ORDER BY lp.media_object_id, d.work_id, f.media_ref;
