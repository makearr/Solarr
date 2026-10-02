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
