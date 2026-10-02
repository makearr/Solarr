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

-- A TV library's series: works with a desired item in the category (visibility is desired-item membership, ADR-27),
-- ordered by sort title with the id as the tie-break.
-- name: ListSeriesInCategory :many
SELECT w.id, w.canonical_title, w.sort_title, w.year, w.added_at, w.description
FROM work w
WHERE w.media_type = 'tv'
  AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = w.id AND d.category_id = sqlc.arg(category_id))
  AND (w.sort_title > sqlc.arg(after_sort) OR (w.sort_title = sqlc.arg(after_sort) AND w.id > sqlc.arg(after_id)))
ORDER BY w.sort_title, w.id
LIMIT sqlc.arg(page_size);

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
SELECT d.id, d.work_id, d.media_ref, d.monitored, d.profile_id, d.required_count, d.present_count, d.state
FROM desired_item d
WHERE d.work_id IN (sqlc.slice(work_ids)) AND d.satisfaction_kind = 'SET' AND d.media_ref <> ''
ORDER BY d.work_id, d.media_sort_key;

-- Which parts of a work have a file, by any item.
-- name: CoveredPartRefs :many
SELECT DISTINCT f.media_ref
FROM desired_item_file f
JOIN desired_item d ON d.id = f.desired_item_id
WHERE d.work_id = ? AND f.role = 'primary' AND f.media_ref <> '';

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

-- Sonarr's Wanted > Missing: monitored episodes that have aired and have no file, newest first.
-- name: ListWantedEpisodes :many
SELECT p.id AS part_id, p.work_id, w.canonical_title AS series_title, p.media_ref, p.title, p.date, p.available_at
FROM work_part p
JOIN work w ON w.id = p.work_id
WHERE w.media_type = 'tv'
  AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = w.id AND d.category_id = sqlc.arg(category_id))
  AND p.monitored = 1
  AND ((p.available_at IS NOT NULL AND p.available_at <= sqlc.arg(now))
       OR (p.available_at IS NULL AND p.date IS NOT NULL AND p.date <= sqlc.arg(today)))
  AND NOT EXISTS (SELECT 1 FROM desired_item_file f JOIN desired_item d2 ON d2.id = f.desired_item_id
                  WHERE d2.work_id = p.work_id AND f.media_ref = p.media_ref AND f.role = 'primary')
  AND (COALESCE(p.date, '') < sqlc.arg(after_date) OR (COALESCE(p.date, '') = sqlc.arg(after_date) AND p.id < sqlc.arg(after_id)))
ORDER BY COALESCE(p.date, '') DESC, p.id DESC
LIMIT sqlc.arg(page_size);
