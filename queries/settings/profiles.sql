-- Quality and language profiles (ADR-40, section 19.8).

-- name: ListQualityProfiles :many
SELECT id, name, rules_json, upgrade_json, suggested_rank, media_type FROM quality_profile
ORDER BY media_type, suggested_rank, name;

-- name: GetQualityProfile :one
SELECT id, name, rules_json, upgrade_json, suggested_rank, media_type FROM quality_profile WHERE id = ?;

-- name: CreateQualityProfile :one
INSERT INTO quality_profile (name, rules_json, upgrade_json, media_type)
VALUES (?, ?, ?, ?)
RETURNING id, name, rules_json, upgrade_json, suggested_rank, media_type;

-- name: UpdateQualityProfile :exec
UPDATE quality_profile SET name = ?, rules_json = ?, upgrade_json = ? WHERE id = ?;

-- name: DeleteQualityProfile :exec
DELETE FROM quality_profile WHERE id = ?;

-- Invariant 41: deleting a profile in use is refused with a count of what references it.
-- name: CountCategoriesUsingQualityProfile :one
SELECT count(*) FROM category WHERE quality_profile_id = ?;

-- name: ListLanguageProfiles :many
SELECT id, name, audio_preferred_json, audio_required_json, subtitle_preferred_json,
       subtitle_required_json, prefer_original_audio, reject_dubbed
FROM language_profile ORDER BY name;

-- name: GetLanguageProfile :one
SELECT id, name, audio_preferred_json, audio_required_json, subtitle_preferred_json,
       subtitle_required_json, prefer_original_audio, reject_dubbed
FROM language_profile WHERE id = ?;

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

-- name: ListCategoryQualityTargets :many
SELECT category_id, audio_codec, container, bitrate_kbps, tolerance_pct, resolution, video_codec, hdr FROM category_quality_target;

-- name: UpsertCategoryQualityTarget :exec
INSERT INTO category_quality_target (category_id, audio_codec, container, bitrate_kbps, tolerance_pct, resolution, video_codec, hdr)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (category_id) DO UPDATE SET
  audio_codec = excluded.audio_codec, container = excluded.container,
  bitrate_kbps = excluded.bitrate_kbps, tolerance_pct = excluded.tolerance_pct,
  resolution = excluded.resolution, video_codec = excluded.video_codec, hdr = excluded.hdr;

-- name: DeleteCategoryQualityTarget :exec
DELETE FROM category_quality_target WHERE category_id = ?;

-- name: CountCategoryListsUsingQualityProfile :one
SELECT CAST(COUNT(*) AS INTEGER) FROM category_quality_profile WHERE quality_profile_id = ?;
