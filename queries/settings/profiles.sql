-- Quality and language profiles (ADR-40, section 19.8).

-- name: ListQualityProfiles :many
SELECT id, name, rules_json, upgrade_json FROM quality_profile ORDER BY name;

-- name: GetQualityProfile :one
SELECT id, name, rules_json, upgrade_json FROM quality_profile WHERE id = ?;

-- name: CreateQualityProfile :one
INSERT INTO quality_profile (name, rules_json, upgrade_json)
VALUES (?, ?, ?)
RETURNING id, name, rules_json, upgrade_json;

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
