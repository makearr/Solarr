-- name: ClearPersonLibrary :exec
DELETE FROM person_library WHERE person_id IN (sqlc.slice('person_ids'));

-- One pass yields a row per role and the any-role row: sqlc expands a slice only once per query.
-- name: FillPersonLibrary :exec
INSERT INTO person_library (person_id, category_id, role, works, latest)
SELECT c.person_id, d.category_id, CASE WHEN k.whole = 1 THEN '' ELSE c.role END AS rollup_role,
       COUNT(DISTINCT c.work_id), MAX(w.year)
FROM credit c
JOIN work w ON w.id = c.work_id
JOIN desired_item d ON d.work_id = c.work_id
CROSS JOIN (SELECT 0 AS whole UNION ALL SELECT 1) k
WHERE c.person_id IN (sqlc.slice('person_ids')) AND c.role <> ''
GROUP BY c.person_id, d.category_id, rollup_role;

-- name: ClearOrganisationLibrary :exec
DELETE FROM organisation_library WHERE organisation_id IN (sqlc.slice('organisation_ids'));

-- name: FillOrganisationLibrary :exec
INSERT INTO organisation_library (organisation_id, category_id, role, works, latest)
SELECT o.organisation_id, d.category_id, CASE WHEN k.whole = 1 THEN '' ELSE o.role END AS rollup_role,
       COUNT(DISTINCT o.work_id), MAX(w.year)
FROM work_organisation o
JOIN work w ON w.id = o.work_id
JOIN desired_item d ON d.work_id = o.work_id
CROSS JOIN (SELECT 0 AS whole UNION ALL SELECT 1) k
WHERE o.organisation_id IN (sqlc.slice('organisation_ids')) AND o.role <> ''
GROUP BY o.organisation_id, d.category_id, rollup_role;

-- name: ListPeopleOfWork :many
SELECT DISTINCT person_id FROM credit WHERE work_id = ? ORDER BY person_id;

-- The libraries holding a person's works, any role, by id.
-- name: ListPersonLibraryCategories :many
SELECT category_id FROM person_library WHERE person_id = ? AND role = '' ORDER BY category_id;

-- name: ListOrganisationsOfWork :many
SELECT DISTINCT organisation_id FROM work_organisation WHERE work_id = ? ORDER BY organisation_id;

-- name: CountDesiredItemsInCategory :one
SELECT CAST(COUNT(*) AS INTEGER) AS items FROM desired_item WHERE work_id = ? AND category_id = ?;

-- name: ListWorkOrganisationIDsInRole :many
SELECT organisation_id FROM work_organisation WHERE work_id = ? AND role = ? ORDER BY organisation_id;

-- name: FillOrganisationDetails :exec
UPDATE organisation SET country = COALESCE(NULLIF(country, ''), sqlc.narg(country)),
  fields_json = json_set(COALESCE(fields_json, '{}'), '$.type', sqlc.arg(type), '$.founded', sqlc.arg(founded), '$.defunct', sqlc.arg(defunct))
WHERE id = sqlc.arg(id);

-- Works of a media type with a provider's id whose organisation in one role was never asked for: no work_field of
-- that role from the asking provider. The edition label is the default (else first) edition's provider id.
-- name: ListWorksToAskOrganisation :many
SELECT w.id AS work_id, x.external_id,
       CAST(COALESCE((SELECT e.label FROM edition e WHERE e.work_id = w.id AND e.kind NOT IN ('unknown', 'withdrawn')
                 ORDER BY e.is_default DESC, e.id LIMIT 1), '') AS TEXT) AS edition_label
FROM work w
JOIN work_external_id x ON x.work_id = w.id AND x.provider = sqlc.arg(external_provider)
WHERE w.media_type = sqlc.arg(media_type) AND w.id > sqlc.arg(after_id)
  AND NOT EXISTS (SELECT 1 FROM work_field f WHERE f.work_id = w.id AND f.field = sqlc.arg(field) AND f.provider = sqlc.arg(asked_provider))
ORDER BY w.id LIMIT sqlc.arg(page_size);

-- Organisations of a kind with an id at a provider whose logo was never asked for.
-- name: ListOrganisationsLogoUnasked :many
SELECT o.id, x.external_id FROM organisation o
JOIN organisation_external_id x ON x.organisation_id = o.id AND x.provider = sqlc.arg(provider)
WHERE o.kind = sqlc.arg(kind) AND o.id > sqlc.arg(after_id) AND json_extract(COALESCE(o.fields_json, '{}'), '$.logo_asked') IS NULL
ORDER BY o.id LIMIT sqlc.arg(page_size);

-- name: SetOrganisationLogoAsked :exec
UPDATE organisation SET fields_json = json_set(COALESCE(fields_json, '{}'), '$.logo_asked', sqlc.arg(asked_at))
WHERE id = sqlc.arg(id);
