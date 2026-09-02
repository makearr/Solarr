-- Physical files. Column lists follow table order, or sqlc stops reusing models.

-- name: InternAttributes :one
INSERT INTO media_attributes (attr_hash, resolution, source, video_codec, hdr,
                              audio_codec, audio_channels, bit_depth, languages,
                              subtitles, edition_tokens, release_group, proper_repack,
                              frame_rate, scan_type, container, lossless, sample_rate,
                              image_format, colour_depth, page_count)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (attr_hash) DO UPDATE SET attr_hash = excluded.attr_hash
RETURNING id, attr_hash, resolution, source, video_codec, hdr, audio_codec,
          audio_channels, bit_depth, languages, subtitles, edition_tokens,
          release_group, proper_repack, frame_rate, scan_type, container,
          lossless, sample_rate, image_format, colour_depth, page_count;

-- name: GetAttributes :one
SELECT id, attr_hash, resolution, source, video_codec, hdr, audio_codec,
       audio_channels, bit_depth, languages, subtitles, edition_tokens,
       release_group, proper_repack, frame_rate, scan_type, container,
       lossless, sample_rate, image_format, colour_depth, page_count
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

-- The second half of importing a file, and execlastid for the same reason.
-- name: AddLibraryPath :execlastid
INSERT INTO library_path (media_object_id, root_folder_id, rel_path, link_kind,
                          is_primary, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ListPathsForObject :many
SELECT id, media_object_id, root_folder_id, rel_path, link_kind, is_primary, created_at
FROM library_path WHERE media_object_id = ? ORDER BY is_primary DESC, id;

-- name: GetPathByLocation :one
SELECT id, media_object_id, root_folder_id, rel_path, link_kind, is_primary, created_at
FROM library_path WHERE root_folder_id = ? AND rel_path = ?;

-- name: DeleteLibraryPath :exec
DELETE FROM library_path WHERE id = ?;

-- A vanished path is MISSING, not deleted, until the root is confirmed online
-- (section 10.4), so the scan lists a root rather than removing from it.
-- name: ListPathsInRoot :many
SELECT id, media_object_id, root_folder_id, rel_path, link_kind, is_primary, created_at
FROM library_path
WHERE root_folder_id = sqlc.arg(root_folder_id) AND id > sqlc.arg(after_id)
ORDER BY id
LIMIT sqlc.arg(page_size);

-- name: DeleteMediaObject :exec
DELETE FROM media_object WHERE id = ?;

-- The root_folder_id half of the ADR-25 orphan check. See CountDesiredItemsByCategory.
-- name: CountLibraryPathsByRootFolder :many
SELECT root_folder_id, CAST(count(*) AS INTEGER) AS total
FROM library_path GROUP BY root_folder_id ORDER BY root_folder_id;
