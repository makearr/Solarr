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

-- The list surface composes its own WHERE in internal/library/works.go: a filter
-- written here as an is-it-empty test is not sargable, so SQLite uses no index for that
-- column and sorts the whole table for every page (documentation/sql-and-sqlc.md).

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

-- name: SetWorkClassification :exec
INSERT INTO work_classification (work_id, kind, value, source)
VALUES (?, ?, ?, ?)
ON CONFLICT (work_id, kind, value) DO UPDATE SET source = excluded.source;

-- name: ListWorkClassifications :many
SELECT work_id, kind, value, source
FROM work_classification WHERE work_id = ? ORDER BY kind, value;

-- Monitoring on an album type is the point: "studio albums, not live".
-- name: FindWorksByClassification :many
SELECT w.id, w.media_type, w.canonical_title, w.sort_title, w.original_language,
       w.year, w.runtime_ms, w.added_at, w.updated_at, w.added_by
FROM work w
JOIN work_classification c ON c.work_id = w.id
WHERE c.kind = sqlc.arg(kind) AND c.value = sqlc.arg(value)
  AND (sqlc.arg(media_type) = '' OR w.media_type = sqlc.arg(media_type))
ORDER BY w.sort_title, w.id
LIMIT sqlc.arg(page_size);

-- name: ClearWorkClassification :exec
DELETE FROM work_classification WHERE work_id = ? AND kind = ?;

-- name: CreateWorkPart :one
INSERT INTO work_part (work_id, media_ref, sort_key, title, runtime_ms, date, monitored)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id, work_id, media_ref, sort_key, title, runtime_ms, date, monitored;

-- name: GetWorkPart :one
SELECT id, work_id, media_ref, sort_key, title, runtime_ms, date, monitored
FROM work_part WHERE id = ?;

-- name: FindWorkPart :one
SELECT id, work_id, media_ref, sort_key, title, runtime_ms, date, monitored
FROM work_part WHERE work_id = ? AND media_ref = ?;

-- Ordered by the module's sort key. Core never parses media_ref to order parts.
-- name: ListWorkParts :many
SELECT id, work_id, media_ref, sort_key, title, runtime_ms, date, monitored
FROM work_part WHERE work_id = ? ORDER BY sort_key, id;

-- What a SET requires follows the monitored parts, so unmonitoring an episode
-- shrinks the season rather than leaving it permanently incomplete (Appendix E, D3).
-- name: CountMonitoredParts :one
SELECT CAST(COUNT(*) AS INTEGER) AS monitored_parts
FROM work_part WHERE work_id = ? AND monitored = 1;

-- name: SetWorkPartMonitored :exec
UPDATE work_part SET monitored = ? WHERE id = ?;

-- name: DeleteWorkPart :exec
DELETE FROM work_part WHERE id = ?;

-- name: LinkWorkPartExternalID :exec
INSERT INTO work_part_external_id (provider, external_id, part_id, confidence)
VALUES (?, ?, ?, ?)
ON CONFLICT (provider, external_id, part_id) DO UPDATE SET
  confidence = excluded.confidence;

-- One recording appears on many albums, so this returns every part sharing the
-- identifier. That is the cross-album duplicate question (ADR-35).
-- name: FindPartsByExternalID :many
SELECT p.id, p.work_id, p.media_ref, p.sort_key, p.title, p.runtime_ms, p.date, p.monitored
FROM work_part p
JOIN work_part_external_id e ON e.part_id = p.id
WHERE e.provider = ? AND e.external_id = ?
ORDER BY p.work_id, p.sort_key, p.id;

-- name: ListWorkPartExternalIDs :many
SELECT provider, external_id, part_id, confidence
FROM work_part_external_id WHERE part_id = ? ORDER BY provider, external_id;

-- name: AddWorkPartTitle :one
INSERT INTO work_part_title (part_id, title, normalized, language, provider)
VALUES (?, ?, ?, ?, ?)
RETURNING id, part_id, title, normalized, language, provider;

-- name: ListWorkPartTitles :many
SELECT id, part_id, title, normalized, language, provider
FROM work_part_title WHERE part_id = ? ORDER BY provider, title;

-- name: AddAltNumbering :exec
INSERT INTO alt_numbering (part_id, scheme, provider, alt_ref)
VALUES (?, ?, ?, ?)
ON CONFLICT (part_id, scheme, provider) DO UPDATE SET
  alt_ref = excluded.alt_ref;

-- Anime absolute numbering: episode 137 of a series resolves to the part that
-- aired numbers do not name (ADR-41). Scoped to the work, or two series collide.
-- name: FindPartByAltNumber :one
SELECT p.id, p.work_id, p.media_ref, p.sort_key, p.title, p.runtime_ms, p.date, p.monitored
FROM work_part p
JOIN alt_numbering a ON a.part_id = p.id
WHERE p.work_id = sqlc.arg(work_id)
  AND a.scheme = sqlc.arg(scheme)
  AND a.alt_ref = sqlc.arg(alt_ref);
