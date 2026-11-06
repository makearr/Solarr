-- Quality and language profiles (ADR-40, section 19.8).

-- name: ListQualityProfiles :many
SELECT id, name, rules_json, upgrade_json, suggested_rank, media_type FROM quality_profile
ORDER BY media_type, suggested_rank, name;

-- name: CreateQualityProfile :one
INSERT INTO quality_profile (name, rules_json, upgrade_json, media_type)
VALUES (?, ?, ?, ?)
RETURNING id, name, rules_json, upgrade_json, suggested_rank, media_type;

-- name: UpdateQualityProfile :exec
UPDATE quality_profile SET name = ?, rules_json = ?, upgrade_json = ? WHERE id = ?;

-- name: DeleteQualityProfile :exec
DELETE FROM quality_profile WHERE id = ?;

-- name: ListLanguageProfiles :many
SELECT id, name, audio_preferred_json, audio_required_json, subtitle_preferred_json,
       subtitle_required_json, prefer_original_audio, reject_dubbed
FROM language_profile ORDER BY name;

-- name: CreateLanguageProfile :one
INSERT INTO language_profile (name, audio_preferred_json, audio_required_json,
                             subtitle_preferred_json, subtitle_required_json,
                             prefer_original_audio, reject_dubbed)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id, name, audio_preferred_json, audio_required_json, subtitle_preferred_json,
          subtitle_required_json, prefer_original_audio, reject_dubbed;

-- name: UpdateLanguageProfile :exec
UPDATE language_profile
SET name = ?, audio_preferred_json = ?, audio_required_json = ?, subtitle_preferred_json = ?,
    subtitle_required_json = ?, prefer_original_audio = ?, reject_dubbed = ?
WHERE id = ?;

-- name: DeleteLanguageProfile :exec
DELETE FROM language_profile WHERE id = ?;

-- name: CountCategoriesUsingLanguageProfile :one
SELECT count(*) FROM category WHERE language_profile_id = ?;

-- name: SetCategoryQualityProfile :exec
UPDATE category SET quality_profile_id = ? WHERE id = ?;

-- name: SetCategoryLanguageProfile :exec
UPDATE category SET language_profile_id = ? WHERE id = ?;

-- name: ListCategoryQualityProfiles :many
SELECT category_id, quality_profile_id, ord FROM category_quality_profile ORDER BY category_id, ord;

-- name: DeleteCategoryQualityProfiles :exec
DELETE FROM category_quality_profile WHERE category_id = ?;

-- name: InsertCategoryQualityProfile :exec
INSERT INTO category_quality_profile (category_id, quality_profile_id, ord) VALUES (?, ?, ?);

-- name: SetCategoryQualityCutoff :exec
UPDATE category SET quality_cutoff_profile_id = ? WHERE id = ?;

-- name: SetCategoryQualityUpgrades :exec
UPDATE category SET quality_upgrades = ? WHERE id = ?;

-- name: SetCategoryProtocolDelay :exec
UPDATE category SET preferred_protocol = ?, protocol_delay_minutes = ? WHERE id = ?;

-- name: ListQualityTargets :many
SELECT id, name, media_type, audio_codec, container, bitrate_kbps, tolerance_pct, resolution, video_codec, hdr,
       audio_channels, audio_languages, subtitle_languages, audio_bitrate_kbps, video_bitrate_kbps, frame_rate, crf,
       prefer_over, ignore_below, label, description, sample_rate_hz, bit_depth, encoder_preset, max_frame_rate, chapters, builtin,
       profile_id, encoder_tune, deinterlace
FROM quality_target ORDER BY media_type, name;

-- name: CreateQualityTarget :one
INSERT INTO quality_target (name, media_type, audio_codec, container, bitrate_kbps, tolerance_pct, resolution, video_codec, hdr,
  audio_channels, audio_languages, subtitle_languages, audio_bitrate_kbps, video_bitrate_kbps, frame_rate, crf, prefer_over, ignore_below,
  label, description, sample_rate_hz, bit_depth, encoder_preset, max_frame_rate, chapters, profile_id, encoder_tune, deinterlace)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: UpdateQualityTarget :exec
UPDATE quality_target SET name = ?, audio_codec = ?, container = ?, bitrate_kbps = ?, tolerance_pct = ?, resolution = ?,
  video_codec = ?, hdr = ?, audio_channels = ?, audio_languages = ?, subtitle_languages = ?, audio_bitrate_kbps = ?,
  video_bitrate_kbps = ?, frame_rate = ?, crf = ?, prefer_over = ?, ignore_below = ?, label = ?, description = ?,
  sample_rate_hz = ?, bit_depth = ?, encoder_preset = ?, max_frame_rate = ?, chapters = ?, profile_id = ?, encoder_tune = ?, deinterlace = ?
WHERE id = ?;

-- name: DeleteQualityTarget :exec
DELETE FROM quality_target WHERE id = ?;

-- name: ListCategoryTargets :many
SELECT category_id, target_id FROM category_target;

-- name: SetCategoryTarget :exec
INSERT INTO category_target (category_id, target_id) VALUES (?, ?)
ON CONFLICT (category_id) DO UPDATE SET target_id = excluded.target_id;

-- name: ClearCategoryTarget :exec
DELETE FROM category_target WHERE category_id = ?;

-- name: CountCategoryListsUsingQualityProfile :one
SELECT CAST(COUNT(*) AS INTEGER) FROM category_quality_profile WHERE quality_profile_id = ?;

-- name: ListCategoryMetadataSources :many
SELECT category_id, source, ord FROM category_metadata_source ORDER BY category_id, ord;

-- name: DeleteCategoryMetadataSources :exec
DELETE FROM category_metadata_source WHERE category_id = ?;

-- name: InsertCategoryMetadataSource :exec
INSERT INTO category_metadata_source (category_id, source, ord) VALUES (?, ?, ?);

-- name: ListCategoryTargetAuto :many
SELECT category_id FROM category_target_auto;

-- name: SetCategoryTargetAuto :exec
INSERT OR IGNORE INTO category_target_auto (category_id) VALUES (?);

-- name: ClearCategoryTargetAuto :exec
DELETE FROM category_target_auto WHERE category_id = ?;
