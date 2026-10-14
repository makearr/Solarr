-- Series (TV works) refresh: provider data written without disturbing what a person set.

-- A refresh never changes monitoring: monitored is left out of the update.
-- name: UpsertWorkPart :one
INSERT INTO work_part (work_id, media_ref, sort_key, title, runtime_ms, date, monitored,
                       available_at, available_confidence, available_basis)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (work_id, media_ref) DO UPDATE SET
  sort_key = excluded.sort_key,
  title = excluded.title,
  runtime_ms = excluded.runtime_ms,
  date = excluded.date,
  available_at = excluded.available_at,
  available_confidence = excluded.available_confidence,
  available_basis = excluded.available_basis
RETURNING id, work_id, media_ref, sort_key, title, runtime_ms, date, monitored, available_at, available_confidence, available_basis;

-- name: DeleteWorkTitlesBySource :exec
DELETE FROM work_title WHERE work_id = ? AND source = ?;

-- name: FindOrganisationByName :one
SELECT id, name, sort_name, kind, parent_id, country, fields_json, monitored
FROM organisation WHERE kind = ? AND name = ? COLLATE NOCASE
ORDER BY id LIMIT 1;

-- name: ClearWorkOrganisationRole :exec
DELETE FROM work_organisation WHERE work_id = ? AND role = ?;

-- A provider fills what a work lacks and corrects what it measured; year and language, once set, stay.
-- name: UpdateWorkSeriesDetails :exec
UPDATE work SET original_language = COALESCE(original_language, sqlc.narg(original_language)),
                year = COALESCE(year, sqlc.narg(year)),
                runtime_ms = COALESCE(sqlc.narg(runtime_ms), runtime_ms),
                description = COALESCE(sqlc.narg(description), description),
                updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: CountWorkFieldManual :one
SELECT CAST(COUNT(*) AS INTEGER) AS manual FROM work_field
WHERE work_id = ? AND field = ? AND provider = '__manual__';

-- name: ListWorkPartRefs :many
SELECT id, media_ref FROM work_part WHERE work_id = ?;

-- name: DeleteWorkRating :exec
DELETE FROM work_rating WHERE work_id = ? AND provider = ?;

-- name: SetWorkRating :exec
INSERT INTO work_rating (work_id, provider, score, votes, fetched_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (work_id, provider) DO UPDATE SET score = excluded.score, votes = excluded.votes, fetched_at = excluded.fetched_at;

-- name: SetWorkCertification :exec
INSERT INTO work_certification (work_id, system, rating)
VALUES (?, ?, ?)
ON CONFLICT (work_id, system) DO UPDATE SET rating = excluded.rating;

-- name: GetSeriesInCategory :one
SELECT w.id, w.canonical_title, w.sort_title, w.year, w.added_at, w.description, w.original_language, w.runtime_ms
FROM work w
WHERE w.id = sqlc.arg(id) AND w.media_type = 'tv'
  AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = w.id AND d.category_id = sqlc.arg(category_id));

-- Per series: its parts, the monitored ones that have aired by the given date and the next air time still to come.
-- Specials (season 0) are left out of every count.
-- name: SeriesPartCounts :many
SELECT p.work_id,
       CAST(COUNT(*) AS INTEGER) AS total,
       CAST(COALESCE(SUM(CASE WHEN p.monitored = 1 THEN 1 ELSE 0 END), 0) AS INTEGER) AS monitored,
       CAST(COALESCE(SUM(CASE WHEN p.monitored = 1 AND p.date IS NOT NULL AND p.date <= sqlc.arg(today) THEN 1 ELSE 0 END), 0) AS INTEGER) AS monitored_aired,
       CAST(COALESCE(MIN(CASE WHEN p.available_at IS NOT NULL AND p.available_at > sqlc.arg(now) THEN p.available_at END), 0) AS INTEGER) AS next_available
FROM work_part p
WHERE p.work_id IN (sqlc.slice(work_ids)) AND p.media_ref NOT LIKE 's00:%'
GROUP BY p.work_id;

-- The season items of the given works, with their counts.
-- name: SeasonItemsForWorks :many
SELECT d.id, d.work_id, d.media_ref, d.monitored, CAST(COALESCE(o.profile_id, d.profile_id) AS INTEGER) AS profile_id,
       d.required_count, d.present_count, d.state
FROM desired_item d
LEFT JOIN desired_profile_override o ON o.desired_item_id = d.id
WHERE d.work_id IN (sqlc.slice(work_ids)) AND d.satisfaction_kind = 'SET' AND d.media_ref <> ''
ORDER BY d.work_id, d.media_sort_key;

-- Which parts of a work have a file still there, by any item.
-- name: CoveredPartRefs :many
SELECT DISTINCT f.media_ref
FROM desired_item_file f
JOIN desired_item d ON d.id = f.desired_item_id
WHERE d.work_id = ? AND f.role = 'primary' AND f.media_ref <> ''
  AND NOT (EXISTS (SELECT 1 FROM library_path gone WHERE gone.media_object_id = f.media_object_id AND gone.missing_since IS NOT NULL)
           AND NOT EXISTS (SELECT 1 FROM library_path held WHERE held.media_object_id = f.media_object_id AND held.missing_since IS NULL));

-- name: SetSeasonPartsMonitored :exec
UPDATE work_part SET monitored = sqlc.arg(monitored) WHERE work_id = sqlc.arg(work_id) AND media_ref LIKE sqlc.arg(scope);

-- Every title a library's series answers to, canonical and alias, for matching folder names (ADR-41).
-- name: ListSeriesTitles :many
SELECT w.id AS work_id, w.canonical_title AS title, w.year AS year
FROM work w
WHERE w.media_type = 'tv'
  AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = w.id AND d.category_id = sqlc.arg(category_id))
UNION ALL
SELECT t.work_id, t.title, w.year
FROM work_title t
JOIN work w ON w.id = t.work_id
WHERE w.media_type = 'tv'
  AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = w.id AND d.category_id = sqlc.arg(category_id));

-- A provider's picture of a person is kept in fields_json and never replaces one already there.
-- name: SetPersonImageURL :exec
UPDATE person SET fields_json = json_set(COALESCE(fields_json, '{}'), '$.image_url', sqlc.arg(image_url))
WHERE id = sqlc.arg(id) AND json_extract(COALESCE(fields_json, '{}'), '$.image_url') IS NULL;

-- name: LinkOrganisationExternalID :exec
INSERT OR IGNORE INTO organisation_external_id (provider, external_id, organisation_id) VALUES (?, ?, ?);

-- name: GetOrganisationByExternalID :one
SELECT o.id, o.name, o.sort_name, o.kind, o.parent_id, o.country, o.fields_json, o.monitored
FROM organisation o
JOIN organisation_external_id e ON e.organisation_id = o.id
WHERE e.provider = ? AND e.external_id = ?;

-- name: SetOrganisationLogo :exec
UPDATE organisation SET fields_json = json_set(COALESCE(fields_json, '{}'), '$.logo', sqlc.arg(logo))
WHERE id = sqlc.arg(id);

-- name: AddWorkOrganisation :exec
INSERT OR IGNORE INTO work_organisation (work_id, organisation_id, role) VALUES (?, ?, ?);

-- name: SetPersonBiography :exec
UPDATE person SET fields_json = json_set(COALESCE(fields_json, '{}'), '$.biography', sqlc.arg(biography))
WHERE id = sqlc.arg(id) AND COALESCE(json_extract(COALESCE(fields_json, '{}'), '$.biography'), '') = '';

-- A part's number under one other scheme ("absolute" for anime), for a whole work.
-- name: ListAltNumbersForWork :many
SELECT a.part_id, a.alt_ref FROM alt_numbering a JOIN work_part p ON p.id = a.part_id
WHERE p.work_id = sqlc.arg(work_id) AND a.scheme = sqlc.arg(scheme);

-- name: DeleteWorkFieldByProvider :exec
DELETE FROM work_field WHERE work_id = ? AND field = ? AND provider = ?;
