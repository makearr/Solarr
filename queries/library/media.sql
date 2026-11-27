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
INSERT INTO media_object (id, size_bytes, quick_hash, content_hash, oshash, phash,
                          audio_fp, audio_fp_prefix, fingerprinted_at,
                          claimed_attributes_id, measured_attributes_id,
                          attribute_mismatch_json, duration_ms, width, height,
                          video_bitrate, audio_bitrate, overall_bitrate,
                          first_seen_at, verified_at, kind)
VALUES ((SELECT MAX(COALESCE((SELECT MAX(id) FROM media_object), 0), COALESCE((SELECT value FROM id_high_water WHERE name = 'media_object'), 0)) + 1), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetMediaObject :one
SELECT id, size_bytes, quick_hash, content_hash, oshash, phash, audio_fp,
       audio_fp_prefix, fingerprinted_at, claimed_attributes_id,
       measured_attributes_id, attribute_mismatch_json, duration_ms, width,
       height, video_bitrate, audio_bitrate, overall_bitrate,
       first_seen_at, verified_at, kind
FROM media_object WHERE id = ?;

-- The cheap identity check an incremental scan runs before reading a file.
-- name: FindMediaObjectsByQuickHash :many
SELECT id, size_bytes, quick_hash, content_hash, oshash, phash, audio_fp,
       audio_fp_prefix, fingerprinted_at, claimed_attributes_id,
       measured_attributes_id, attribute_mismatch_json, duration_ms, width,
       height, video_bitrate, audio_bitrate, overall_bitrate,
       first_seen_at, verified_at, kind
FROM media_object WHERE quick_hash = ?;

-- oshash is exact identity: the same bytes, with no decoding (ADR-35).
-- name: GetMediaObjectByOshash :one
SELECT id, size_bytes, quick_hash, content_hash, oshash, phash, audio_fp,
       audio_fp_prefix, fingerprinted_at, claimed_attributes_id,
       measured_attributes_id, attribute_mismatch_json, duration_ms, width,
       height, video_bitrate, audio_bitrate, overall_bitrate,
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
    verified_at = ?
WHERE id = ?;

-- What the probe read of one file, replacing an earlier probe.
-- name: SetMediaProbe :exec
INSERT INTO media_probe (media_object_id, probe_version, probed_at) VALUES (?, ?, ?)
ON CONFLICT (media_object_id) DO UPDATE SET
  probe_version = excluded.probe_version, probed_at = excluded.probed_at;

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
       height, video_bitrate, audio_bitrate, overall_bitrate,
       first_seen_at, verified_at, kind
FROM media_object
WHERE fingerprinted_at IS NULL AND id > sqlc.arg(after_id)
ORDER BY id
LIMIT sqlc.arg(page_size);

-- name: SetFingerprints :exec
UPDATE media_object
SET oshash = ?, phash = ?, audio_fp = ?, audio_fp_prefix = ?, fingerprinted_at = ?
WHERE id = ?;

-- The measured half of ADR-39 is the lowest-priority background work, resumable
-- the same way fingerprinting is (todo 4.2): a cursor on media_object.id rather
-- than re-deriving "unmeasured" from scratch, so a file that fails its probe
-- this run does not block the cursor from ever reaching what comes after it.
-- The join to library_path is within library.db, not across databases --
-- both tables live here, so it does not run into the no-cross-DB-join rule.
-- name: ListUnmeasured :many
SELECT o.id, o.size_bytes, lp.root_folder_id, lp.rel_path
FROM media_object o
JOIN library_path lp ON lp.id = (SELECT MIN(id) FROM library_path WHERE media_object_id = o.id AND is_primary = 1 AND missing_since IS NULL)
WHERE o.measured_attributes_id IS NULL
  AND COALESCE(o.kind, '') <> 'book' AND NOT EXISTS (SELECT 1 FROM media_probe_failure f WHERE f.media_object_id = o.id AND f.size_bytes = o.size_bytes AND f.retry_at > sqlc.arg(now))
  AND o.id > sqlc.arg(after_id)
ORDER BY o.id
LIMIT sqlc.arg(page_size);

-- Files whose probe predates what the probe now reads of every file (below_version), read once more.
-- name: ListStaleProbes :many
SELECT o.id, o.size_bytes, lp.root_folder_id, lp.rel_path
FROM media_probe mp
JOIN media_object o ON o.id = mp.media_object_id
JOIN library_path lp ON lp.id = (SELECT MIN(id) FROM library_path WHERE media_object_id = o.id AND is_primary = 1 AND missing_since IS NULL)
WHERE mp.probe_version < sqlc.arg(below_version) AND o.measured_attributes_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM media_probe_failure f WHERE f.media_object_id = o.id AND f.size_bytes = o.size_bytes AND f.retry_at > sqlc.arg(now)) AND o.id > sqlc.arg(after_id)
ORDER BY o.id
LIMIT sqlc.arg(page_size);

-- Video files whose probe predates what the probe now reads of a video (below_version): the picture, the subtitle
-- tracks and the encoder's CRF.
-- name: ListStaleVideoProbes :many
SELECT o.id, o.size_bytes, lp.root_folder_id, lp.rel_path
FROM media_probe mp
JOIN media_object o ON o.id = mp.media_object_id
JOIN library_path lp ON lp.id = (SELECT MIN(id) FROM library_path WHERE media_object_id = o.id AND is_primary = 1 AND missing_since IS NULL)
WHERE mp.probe_version < sqlc.arg(below_version) AND o.measured_attributes_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM media_probe_failure f WHERE f.media_object_id = o.id AND f.size_bytes = o.size_bytes AND f.retry_at > sqlc.arg(now)) AND o.id > sqlc.arg(after_id)
  AND o.kind = 'video'
ORDER BY o.id
LIMIT sqlc.arg(page_size);

-- Video files scanned before oshash was written at scan time; under 64 KiB the hash is undefined, so those never list.
-- name: ListVideoWithoutOshash :many
SELECT o.id, o.size_bytes, lp.root_folder_id, lp.rel_path
FROM media_object o
JOIN library_path lp ON lp.media_object_id = o.id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE o.oshash IS NULL AND o.kind = 'video' AND o.size_bytes >= 65536 AND o.id > sqlc.arg(after_id)
ORDER BY o.id
LIMIT sqlc.arg(page_size);

-- Audio files with no raw fingerprint yet. An empty one means fpcalc could not read the file; it is asked again only
-- when the file's bytes change (SetMediaObjectContent clears it).
-- name: ListAudioWithoutFingerprint :many
SELECT o.id, o.size_bytes, lp.root_folder_id, lp.rel_path
FROM media_object o
JOIN library_path lp ON lp.media_object_id = o.id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE o.audio_fp IS NULL AND o.kind = 'audio' AND o.id > sqlc.arg(after_id)
ORDER BY o.id
LIMIT sqlc.arg(page_size);

-- How many audio files of the roots have a fingerprint, and how many are still waiting for the sweep.
-- name: CountAudioFingerprints :one
SELECT CAST(COUNT(*) AS INTEGER) AS total, CAST(COALESCE(SUM(CASE WHEN o.audio_fp IS NULL THEN 1 ELSE 0 END), 0) AS INTEGER) AS pending
FROM media_object o
WHERE o.kind = 'audio' AND EXISTS (SELECT 1 FROM library_path lp WHERE lp.media_object_id = o.id AND lp.is_primary = 1 AND lp.missing_since IS NULL
                                    AND lp.root_folder_id IN (SELECT value FROM json_each(sqlc.arg(root_folder_ids))));

-- name: SetAudioFingerprint :exec
UPDATE media_object SET audio_fp = ?, audio_fp_prefix = ? WHERE id = ?;

-- The files the probe sweep still has to read, counted for its progress: ListUnmeasured, ListStaleProbes,
-- ListStaleVideoProbes and ListVideoWithoutOshash without their page.
-- name: CountUnmeasured :one
SELECT CAST(count(*) AS INTEGER) AS total
FROM media_object o
JOIN library_path lp ON lp.id = (SELECT MIN(id) FROM library_path WHERE media_object_id = o.id AND is_primary = 1 AND missing_since IS NULL)
WHERE o.measured_attributes_id IS NULL
  AND COALESCE(o.kind, '') <> 'book' AND NOT EXISTS (SELECT 1 FROM media_probe_failure f WHERE f.media_object_id = o.id AND f.size_bytes = o.size_bytes AND f.retry_at > sqlc.arg(now));

-- name: CountStaleProbes :one
SELECT CAST(count(*) AS INTEGER) AS total
FROM media_probe mp
JOIN media_object o ON o.id = mp.media_object_id
JOIN library_path lp ON lp.id = (SELECT MIN(id) FROM library_path WHERE media_object_id = o.id AND is_primary = 1 AND missing_since IS NULL)
WHERE mp.probe_version < sqlc.arg(below_version) AND o.measured_attributes_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM media_probe_failure f WHERE f.media_object_id = o.id AND f.size_bytes = o.size_bytes AND f.retry_at > sqlc.arg(now));

-- name: CountStaleVideoProbes :one
SELECT CAST(count(*) AS INTEGER) AS total
FROM media_probe mp
JOIN media_object o ON o.id = mp.media_object_id
JOIN library_path lp ON lp.id = (SELECT MIN(id) FROM library_path WHERE media_object_id = o.id AND is_primary = 1 AND missing_since IS NULL)
WHERE mp.probe_version < sqlc.arg(below_version) AND o.measured_attributes_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM media_probe_failure f WHERE f.media_object_id = o.id AND f.size_bytes = o.size_bytes AND f.retry_at > sqlc.arg(now)) AND o.kind = 'video';

-- name: CountVideoWithoutOshash :one
SELECT CAST(count(*) AS INTEGER) AS total
FROM media_object o
JOIN library_path lp ON lp.media_object_id = o.id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE o.oshash IS NULL AND o.kind = 'video' AND o.size_bytes >= 65536;

-- name: SetOshash :exec
UPDATE media_object SET oshash = ? WHERE id = ?;

-- Video files with no per-frame hash yet, those hashed before it included, and (when previews are wanted) those with no
-- preview of the current algorithm: the lowest-priority pass of the probe sweep (ADR-35).
-- name: ListVideoWithoutFrameHashes :many
SELECT o.id, o.size_bytes, lp.root_folder_id, lp.rel_path
FROM media_object o
JOIN library_path lp ON lp.media_object_id = o.id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE o.kind = 'video' AND o.id > sqlc.arg(after_id)
  AND (NOT EXISTS (SELECT 1 FROM video_frame_hash f WHERE f.media_object_id = o.id AND f.algorithm_version = sqlc.arg(version))
       OR (CAST(sqlc.arg(want_previews) AS INTEGER) = 1
           AND NOT EXISTS (SELECT 1 FROM video_preview v WHERE v.media_object_id = o.id AND v.algorithm_version = sqlc.arg(version))))
ORDER BY o.id
LIMIT sqlc.arg(page_size);

-- The same videos as ListVideoWithoutFrameHashes, counted, for the progress of the pass.
-- name: CountVideoWithoutFrameHashes :one
SELECT CAST(count(*) AS INTEGER) AS total
FROM media_object o
JOIN library_path lp ON lp.media_object_id = o.id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE o.kind = 'video'
  AND (NOT EXISTS (SELECT 1 FROM video_frame_hash f WHERE f.media_object_id = o.id AND f.algorithm_version = sqlc.arg(version))
       OR (CAST(sqlc.arg(want_previews) AS INTEGER) = 1
           AND NOT EXISTS (SELECT 1 FROM video_preview v WHERE v.media_object_id = o.id AND v.algorithm_version = sqlc.arg(version))));

-- name: SetVideoPreview :exec
INSERT INTO video_preview (media_object_id, algorithm_version, has_frame, has_sheet, made_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (media_object_id) DO UPDATE SET algorithm_version = excluded.algorithm_version, has_frame = excluded.has_frame,
  has_sheet = excluded.has_sheet, made_at = excluded.made_at;

-- name: SetPhash :exec
UPDATE media_object SET phash = ? WHERE id = ?;

-- name: SetVideoFrameHashes :exec
INSERT INTO video_frame_hash (media_object_id, frames, algorithm_version) VALUES (?, ?, ?)
ON CONFLICT (media_object_id) DO UPDATE SET frames = excluded.frames, algorithm_version = excluded.algorithm_version;

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

-- A file changed in place is the same object with new bytes: what was derived from the old bytes is cleared so the
-- fingerprint and probe jobs measure it again.
-- name: SetMediaObjectContent :exec
UPDATE media_object
SET size_bytes = ?, quick_hash = ?, oshash = ?, content_hash = NULL, phash = NULL, audio_fp = NULL,
    audio_fp_prefix = NULL, fingerprinted_at = NULL, measured_attributes_id = NULL
WHERE id = ?;

-- A file whose tags were rewritten in place has new bytes under the same identity: only what names the bytes changes.
-- name: SetMediaObjectBytes :exec
UPDATE media_object SET size_bytes = ?, quick_hash = ? WHERE id = ?;

-- name: SetLibraryPathObject :exec
UPDATE library_path SET media_object_id = ? WHERE id = ?;

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

-- One root's present files by name, paged on the unique (root_folder_id, rel_path) index so no page sorts the root.
-- name: ListPresentRelPathsInRoot :many
SELECT rel_path, created_at
FROM library_path
WHERE root_folder_id = sqlc.arg(root_folder_id) AND rel_path > sqlc.arg(after_rel_path) AND missing_since IS NULL
ORDER BY rel_path
LIMIT sqlc.arg(page_size);

-- ADR-51's drain: every real file to evacuate, with its size for
-- mover.PlanDrain's own affinity-group byte totals. Primary paths only,
-- the same convention ListUnmeasured already uses -- a secondary,
-- cross-entry-hardlinked path for the same object (ADR-11) sharing this
-- root is a known, narrow gap move_item's own (move_job_id, media_object_id)
-- primary key already forces (one item per object per job), not something
-- this query can widen on its own.
-- name: ListPathsForRootWithSize :many
SELECT lp.id, lp.media_object_id, lp.rel_path, o.size_bytes
FROM library_path lp
JOIN media_object o ON o.id = lp.media_object_id
WHERE lp.root_folder_id = sqlc.arg(root_folder_id) AND lp.is_primary = 1
  AND lp.id > sqlc.arg(after_id)
ORDER BY lp.id
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

-- A file that moved to another root: its works are on disk there now, and no longer in the old root unless another of
-- their files still is.
-- name: LinkWorksOfPathToRoot :exec
INSERT OR IGNORE INTO work_root_folder (work_id, root_folder_id, first_seen_at)
SELECT wf.work_id, lp.root_folder_id, lp.last_seen_at
FROM library_path lp JOIN work_file wf ON wf.media_object_id = lp.media_object_id
WHERE lp.root_folder_id = sqlc.arg(root_folder_id) AND lp.rel_path = sqlc.arg(rel_path);

-- name: UnlinkWorksOfPathFromRoot :exec
DELETE FROM work_root_folder
WHERE work_root_folder.root_folder_id = sqlc.arg(old_root_folder_id)
  AND work_root_folder.work_id IN (SELECT wf.work_id FROM library_path lp JOIN work_file wf ON wf.media_object_id = lp.media_object_id
                  WHERE lp.root_folder_id = sqlc.arg(new_root_folder_id) AND lp.rel_path = sqlc.arg(new_rel_path))
  AND NOT EXISTS (SELECT 1 FROM work_file other JOIN library_path held ON held.media_object_id = other.media_object_id
                  WHERE other.work_id = work_root_folder.work_id AND held.root_folder_id = work_root_folder.root_folder_id
                    AND held.missing_since IS NULL);

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

-- Direct and deeper children of a directory, by the (root_folder_id, rel_path) index range: prefix is "dir/" and
-- prefix_end is "dir0" ('0' is the byte after '/').
-- name: PresentPathsInDirectory :many
SELECT id, rel_path FROM library_path
WHERE root_folder_id = sqlc.arg(root_folder_id)
  AND rel_path >= sqlc.arg(prefix) AND rel_path < sqlc.arg(prefix_end)
  AND missing_since IS NULL;

-- name: PresentPathsAtRootLevel :many
SELECT id, rel_path FROM library_path
WHERE root_folder_id = ? AND instr(rel_path, '/') = 0 AND missing_since IS NULL;

-- name: MarkLibraryPathsMissingByID :execrows
UPDATE library_path SET missing_since = sqlc.arg(missing_since)
WHERE id IN (sqlc.slice(ids)) AND missing_since IS NULL;

-- name: DeleteMediaObject :exec
DELETE FROM media_object WHERE id = ?;

-- The root_folder_id half of the ADR-25 orphan check. See CountDesiredItemsByCategory.
-- name: CountLibraryPathsByRootFolder :many
SELECT root_folder_id, CAST(count(*) AS INTEGER) AS total
FROM library_path GROUP BY root_folder_id ORDER BY root_folder_id;

-- The quality profile half of the same check: the profile an item chose for itself.
-- name: CountProfileOverridesByProfile :many
SELECT profile_id, CAST(count(*) AS INTEGER) AS total
FROM desired_profile_override GROUP BY profile_id ORDER BY profile_id;

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

-- Whether the library still has a file on a root that is not missing: an empty root then is an unmounted disk.
-- name: RootHoldsFiles :one
SELECT CAST(EXISTS (SELECT 1 FROM library_path WHERE root_folder_id = ? AND missing_since IS NULL) AS INTEGER) AS holds;

-- Bytes the library holds on a root: each file once, at its primary path.
-- name: RootLibraryBytes :one
SELECT CAST(COALESCE(SUM(o.size_bytes), 0) AS INTEGER) AS bytes
FROM library_path lp JOIN media_object o ON o.id = lp.media_object_id
WHERE lp.root_folder_id = ? AND lp.is_primary = 1 AND lp.missing_since IS NULL;

-- Every video with frame hashes and its primary path; the similar-video report brute-forces Hamming distance in Go (ADR-35).
-- name: ListHashedVideos :many
SELECT o.id, o.size_bytes, o.phash, o.duration_ms, o.width, o.height, lp.root_folder_id, lp.rel_path, f.frames
FROM media_object o
JOIN video_frame_hash f ON f.media_object_id = o.id
JOIN library_path lp ON lp.media_object_id = o.id AND lp.is_primary = 1 AND lp.missing_since IS NULL
WHERE o.phash IS NOT NULL AND o.kind = 'video' AND f.algorithm_version = sqlc.arg(version)
ORDER BY o.id;

-- What the duplicate screens show of videos: their measured facts and the work each belongs to. ids is a JSON array of media object ids.
-- name: ListVideoFacts :many
SELECT o.id, o.overall_bitrate, o.audio_bitrate, o.video_bitrate, o.size_bytes, o.height, o.duration_ms,
       a.audio_codec, a.video_codec, a.container, a.lossless, a.languages,
       a.resolution, a.hdr, a.audio_channels, a.subtitles, a.frame_rate, a.crf,
       CAST(COALESCE((SELECT wf.work_id FROM work_file wf WHERE wf.media_object_id = o.id ORDER BY wf.work_id LIMIT 1), 0) AS INTEGER) AS work_id,
       CAST(COALESCE((SELECT w.canonical_title FROM work_file wf JOIN work w ON w.id = wf.work_id WHERE wf.media_object_id = o.id ORDER BY wf.work_id LIMIT 1), '') AS TEXT) AS work_title
FROM media_object o
LEFT JOIN media_attributes a ON a.id = o.measured_attributes_id
WHERE o.id IN (SELECT value FROM json_each(sqlc.arg(ids)))
ORDER BY o.id;

-- Every live path of a work's files, with how each was placed.
-- name: ListLivePathsForWork :many
SELECT wf.media_object_id, lp.root_folder_id, lp.rel_path, lp.link_kind, lp.is_primary
FROM work_file wf
JOIN library_path lp ON lp.media_object_id = wf.media_object_id AND lp.missing_since IS NULL
WHERE wf.work_id = ?
ORDER BY wf.media_object_id, lp.is_primary DESC, lp.id;

-- Other files with the same bytes (size and head and tail sample) as each of a work's files.
-- name: ListSameBytesPeersForWork :many
SELECT m.id AS media_object_id, CAST(COUNT(x.id) AS INTEGER) AS peers
FROM work_file wf
JOIN media_object m ON m.id = wf.media_object_id
JOIN media_object x ON x.quick_hash = m.quick_hash AND x.size_bytes = m.size_bytes AND x.id <> m.id
WHERE wf.work_id = ?
GROUP BY m.id;

-- Every file that shows the same mean picture as one of a work's videos; the caller counts them per hash.
-- name: ListSamePicturePeersForWork :many
SELECT x.id, x.phash
FROM media_object x
WHERE x.phash IN (SELECT o.phash FROM work_file wf JOIN media_object o ON o.id = wf.media_object_id WHERE wf.work_id = ? AND o.phash IS NOT NULL);

-- The hashed videos of one work: an import compares a download's pictures with what the work already holds.
-- name: ListWorkVideoHashes :many
SELECT o.id, o.phash, o.duration_ms, f.frames
FROM work_file wf
JOIN media_object o ON o.id = wf.media_object_id
JOIN video_frame_hash f ON f.media_object_id = o.id
WHERE wf.work_id = sqlc.arg(work_id) AND o.phash IS NOT NULL AND o.kind = 'video' AND f.algorithm_version = sqlc.arg(version)
ORDER BY o.id;

-- The hashed primary video of each episode or part: crossover candidates are tested against each other with it.
-- name: ListPartVideoHashes :many
SELECT wp.id AS part_id, o.id, o.phash, o.duration_ms, f.frames
FROM work_part wp
JOIN desired_item d ON d.work_id = wp.work_id
JOIN desired_item_file df ON df.desired_item_id = d.id AND df.media_ref = wp.media_ref AND df.role = 'primary'
JOIN media_object o ON o.id = df.media_object_id
JOIN video_frame_hash f ON f.media_object_id = o.id
WHERE wp.id IN (sqlc.slice(part_ids)) AND o.phash IS NOT NULL AND o.kind = 'video' AND f.algorithm_version = sqlc.arg(version)
ORDER BY wp.id, o.id;

-- The download a placed file came in with, nothing when a person imported it by hand.
-- name: GetAcquisitionIDOfMediaObject :one
SELECT s.acquisition_id
FROM import_unit_file iuf
JOIN import_unit u ON u.id = iuf.unit_id
JOIN import_session s ON s.id = u.session_id
WHERE iuf.media_object_id = ? AND s.acquisition_id IS NOT NULL
ORDER BY s.id DESC
LIMIT 1;

-- name: NoteMissingRecount :exec
INSERT OR IGNORE INTO missing_recount (root_folder_id, missing_since) VALUES (?, ?);

-- name: ListMissingRecounts :many
SELECT root_folder_id, missing_since FROM missing_recount ORDER BY root_folder_id, missing_since;

-- name: DeleteMissingRecount :exec
DELETE FROM missing_recount WHERE root_folder_id = ? AND missing_since = ?;

-- name: UpsertProbeFailure :exec
INSERT INTO media_probe_failure (media_object_id, attempts, last_error, last_attempt_at, retry_at, size_bytes, raw)
VALUES (sqlc.arg(media_object_id), 1, sqlc.arg(last_error), sqlc.arg(at), sqlc.arg(retry_at), sqlc.arg(size_bytes), sqlc.arg(raw))
ON CONFLICT (media_object_id) DO UPDATE SET
  attempts = media_probe_failure.attempts + 1, last_error = excluded.last_error, raw = excluded.raw,
  last_attempt_at = excluded.last_attempt_at, retry_at = excluded.retry_at, size_bytes = excluded.size_bytes;

-- name: GetProbeFailureRaw :one
SELECT raw FROM media_probe_failure WHERE media_object_id = ?;

-- name: GetProbeFailureAttempts :one
SELECT attempts FROM media_probe_failure WHERE media_object_id = ?;

-- name: DeleteProbeFailure :exec
DELETE FROM media_probe_failure WHERE media_object_id = ?;

-- name: DeleteAllProbeFailures :execrows
DELETE FROM media_probe_failure;

-- name: CountProbeFailures :one
SELECT CAST(count(*) AS INTEGER) AS total FROM media_probe_failure;
