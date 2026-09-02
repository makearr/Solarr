-- Identity queries. Column lists follow table order, or sqlc stops reusing models.

-- name: CreateWork :one
INSERT INTO work (media_type, canonical_title, sort_title, original_language,
                  year, runtime_ms, added_at, updated_at, added_by)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, media_type, canonical_title, sort_title, original_language,
          year, runtime_ms, added_at, updated_at, added_by;

-- name: GetWork :one
SELECT id, media_type, canonical_title, sort_title, original_language,
       year, runtime_ms, added_at, updated_at, added_by
FROM work WHERE id = ?;

-- Resolving a provider id to a work is the first step of the Identifier chain (ADR-24).
-- name: GetWorkByExternalID :one
SELECT w.id, w.media_type, w.canonical_title, w.sort_title, w.original_language,
       w.year, w.runtime_ms, w.added_at, w.updated_at, w.added_by
FROM work w
JOIN work_external_id e ON e.work_id = w.id
WHERE e.provider = ? AND e.external_id = ?;

-- Cursor pagination, never OFFSET (Invariant 32). The id tie-break is not optional:
-- sort_title is not unique, so a page boundary inside a group would drop or repeat rows.
-- name: ListWorksPage :many
SELECT id, media_type, canonical_title, sort_title, original_language,
       year, runtime_ms, added_at, updated_at, added_by
FROM work
WHERE (sqlc.arg(media_type) = '' OR media_type = sqlc.arg(media_type))
  AND (sqlc.arg(after_sort) = ''
       OR sort_title > sqlc.arg(after_sort)
       OR (sort_title = sqlc.arg(after_sort) AND id > sqlc.arg(after_id)))
ORDER BY sort_title, id
LIMIT sqlc.arg(page_size);

-- name: UpdateWorkTimestamp :exec
UPDATE work SET updated_at = ? WHERE id = ?;

-- name: DeleteWork :exec
DELETE FROM work WHERE id = ?;

-- name: LinkWorkExternalID :exec
INSERT INTO work_external_id (provider, external_id, work_id, confidence, first_seen_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (provider, external_id) DO UPDATE SET
  work_id = excluded.work_id,
  confidence = excluded.confidence;

-- name: ListWorkExternalIDs :many
SELECT provider, external_id, work_id, confidence, first_seen_at
FROM work_external_id WHERE work_id = ? ORDER BY provider, external_id;

-- name: AddWorkTitle :one
INSERT INTO work_title (work_id, title, normalized, language, kind, source)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id, work_id, title, normalized, language, kind, source;

-- name: ListWorkTitles :many
SELECT id, work_id, title, normalized, language, kind, source
FROM work_title WHERE work_id = ? ORDER BY kind, title;

-- Every stored variant is matched during import, not just the canonical one (ADR-41).
-- name: FindWorksByNormalizedTitle :many
SELECT w.id, w.media_type, w.canonical_title, w.sort_title, w.original_language,
       w.year, w.runtime_ms, w.added_at, w.updated_at, w.added_by
FROM work w
JOIN work_title t ON t.work_id = w.id
WHERE t.normalized = sqlc.arg(normalized)
  AND (sqlc.arg(media_type) = '' OR w.media_type = sqlc.arg(media_type))
GROUP BY w.id
ORDER BY w.sort_title, w.id
LIMIT sqlc.arg(page_size);

-- name: CreateEdition :one
INSERT INTO edition (work_id, kind, label, year, format, catalogue_number,
                     barcode, runtime_ms, is_default)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, work_id, kind, label, year, format, catalogue_number,
          barcode, runtime_ms, is_default;

-- name: ListEditions :many
SELECT id, work_id, kind, label, year, format, catalogue_number,
       barcode, runtime_ms, is_default
FROM edition WHERE work_id = ? ORDER BY is_default DESC, year, label;

-- name: GetEditionByExternalID :one
SELECT e.id, e.work_id, e.kind, e.label, e.year, e.format, e.catalogue_number,
       e.barcode, e.runtime_ms, e.is_default
FROM edition e
JOIN edition_external_id x ON x.edition_id = e.id
WHERE x.provider = ? AND x.external_id = ?;

-- name: LinkEditionExternalID :exec
INSERT INTO edition_external_id (provider, external_id, edition_id, confidence)
VALUES (?, ?, ?, ?)
ON CONFLICT (provider, external_id) DO UPDATE SET
  edition_id = excluded.edition_id,
  confidence = excluded.confidence;

-- name: SetWorkField :exec
INSERT INTO work_field (work_id, field, provider, value, fetched_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (work_id, field, provider) DO UPDATE SET
  value = excluded.value,
  fetched_at = excluded.fetched_at;

-- name: ListWorkFields :many
SELECT work_id, field, provider, value, fetched_at
FROM work_field WHERE work_id = ? ORDER BY field, provider;

-- name: SetWorkDate :exec
INSERT INTO work_date (work_id, kind, region, date, source)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (work_id, kind, region) DO UPDATE SET
  date = excluded.date,
  source = excluded.source;

-- name: ListWorkDates :many
SELECT work_id, kind, region, date, source
FROM work_date WHERE work_id = ? ORDER BY kind, region;
