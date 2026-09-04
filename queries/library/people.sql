-- People, organisations, tags and subscriptions. Column lists follow table order.

-- name: CreatePerson :one
INSERT INTO person (name, sort_name, disambiguation, kind, gender, country,
                    born, died, fields_json, monitored, added_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, name, sort_name, disambiguation, kind, gender, country, born, died,
          fields_json, monitored, added_at;

-- name: GetPerson :one
SELECT id, name, sort_name, disambiguation, kind, gender, country, born, died,
       fields_json, monitored, added_at
FROM person WHERE id = ?;

-- name: SetPersonMonitored :exec
UPDATE person SET monitored = ? WHERE id = ?;

-- name: AddPersonAlias :exec
INSERT INTO person_alias (person_id, alias, normalized, source)
VALUES (?, ?, ?, ?)
ON CONFLICT (person_id, alias) DO UPDATE SET
  normalized = excluded.normalized,
  source = excluded.source;

-- A stage name is often the only way to connect a release to a person (ADR-32).
-- name: FindPeopleByAlias :many
SELECT p.id, p.name, p.sort_name, p.disambiguation, p.kind, p.gender, p.country,
       p.born, p.died, p.fields_json, p.monitored, p.added_at
FROM person p
JOIN person_alias a ON a.person_id = p.id
WHERE a.normalized = ? ORDER BY p.sort_name, p.id;

-- name: LinkPersonExternalID :exec
INSERT INTO person_external_id (provider, external_id, person_id)
VALUES (?, ?, ?)
ON CONFLICT (provider, external_id) DO UPDATE SET person_id = excluded.person_id;

-- name: GetPersonByExternalID :one
SELECT p.id, p.name, p.sort_name, p.disambiguation, p.kind, p.gender, p.country,
       p.born, p.died, p.fields_json, p.monitored, p.added_at
FROM person p
JOIN person_external_id e ON e.person_id = p.id
WHERE e.provider = ? AND e.external_id = ?;

-- edition_id and part_id are 0 for a credit on the work itself (Appendix E, D2).
-- name: AddCredit :exec
INSERT INTO credit (person_id, work_id, edition_id, part_id, role, character, ord)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (person_id, work_id, edition_id, part_id, role, character) DO UPDATE SET
  ord = excluded.ord;

-- The work itself only. A narrator credited on one audiobook edition, or a soloist
-- on one track, is not a credit on the work and must not appear here.
-- name: ListCreditsForWork :many
SELECT person_id, work_id, edition_id, part_id, role, character, ord
FROM credit
WHERE work_id = ? AND edition_id = 0 AND part_id = 0
ORDER BY role, ord, person_id;

-- The work detail page's own need: a credit is not useful to display without
-- the person's name, and a second round-trip per credit is not worth saving
-- a join for.
-- name: ListCreditsForWorkWithPerson :many
SELECT c.person_id, c.role, c.ord, p.name AS person_name
FROM credit c
JOIN person p ON p.id = c.person_id
WHERE c.work_id = ? AND c.edition_id = 0 AND c.part_id = 0
ORDER BY c.role, c.ord, p.sort_name;

-- name: ListCreditsForEdition :many
SELECT person_id, work_id, edition_id, part_id, role, character, ord
FROM credit WHERE edition_id = ? ORDER BY role, ord, person_id;

-- Per-track performers and composers, which is what classical and various-artists
-- releases need and what a work-level credit cannot express.
-- name: ListCreditsForPart :many
SELECT person_id, work_id, edition_id, part_id, role, character, ord
FROM credit WHERE part_id = ? ORDER BY role, ord, person_id;

-- Every credit anywhere under a work, at any scope. Coverage counts use this;
-- the work-level listing above deliberately does not.
-- name: ListAllCreditsUnderWork :many
SELECT person_id, work_id, edition_id, part_id, role, character, ord
FROM credit WHERE work_id = ? ORDER BY part_id, edition_id, role, ord, person_id;

-- "Htuacmasn as director, not producer" is a role-scoped query, which is why the
-- role lives on the credit (F13).
-- name: ListWorksCreditedTo :many
SELECT w.id, w.media_type, w.canonical_title, w.sort_title, w.original_language,
       w.year, w.runtime_ms, w.added_at, w.updated_at, w.added_by
FROM work w
JOIN credit c ON c.work_id = w.id
WHERE c.person_id = sqlc.arg(person_id)
  AND (sqlc.arg(role) = '' OR c.role = sqlc.arg(role))
ORDER BY w.year, w.sort_title, w.id;

-- Coverage: what a monitored person has produced against what is held (ADR-32).
-- name: CountWorksCreditedTo :one
SELECT CAST(COUNT(DISTINCT c.work_id) AS INTEGER) AS total
FROM credit c
WHERE c.person_id = sqlc.arg(person_id)
  AND (sqlc.arg(role) = '' OR c.role = sqlc.arg(role));

-- name: CreateOrganisation :one
INSERT INTO organisation (name, sort_name, kind, parent_id, country, fields_json, monitored)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id, name, sort_name, kind, parent_id, country, fields_json, monitored;

-- name: GetOrganisation :one
SELECT id, name, sort_name, kind, parent_id, country, fields_json, monitored
FROM organisation WHERE id = ?;

-- "Monitor this studio and everything beneath it" needs the whole subtree, and
-- imprints and sub-labels are real in every domain (ADR-32).
-- name: ListOrganisationSubtree :many
WITH RECURSIVE subtree(org_id) AS (
  SELECT organisation.id FROM organisation WHERE organisation.id = ?
  UNION
  SELECT o.id FROM organisation o JOIN subtree ON o.parent_id = subtree.org_id
)
SELECT o.id, o.name, o.sort_name, o.kind, o.parent_id, o.country, o.fields_json, o.monitored
FROM organisation o JOIN subtree ON subtree.org_id = o.id
ORDER BY o.sort_name, o.id;

-- name: LinkWorkOrganisation :exec
INSERT INTO work_organisation (work_id, organisation_id, role)
VALUES (?, ?, ?)
ON CONFLICT (work_id, organisation_id, role) DO NOTHING;

-- name: ListOrganisationsForWork :many
SELECT work_id, organisation_id, role
FROM work_organisation WHERE work_id = ? ORDER BY role, organisation_id;

-- name: CreateTag :one
INSERT INTO tag (name, slug, parent_id, description, source)
VALUES (?, ?, ?, ?, ?)
RETURNING id, name, slug, parent_id, description, source;

-- name: GetTagBySlug :one
SELECT id, name, slug, parent_id, description, source FROM tag WHERE slug = ?;

-- The closure is materialised, so inserting a tag inserts its ancestry too.
-- name: AddTagClosure :exec
INSERT INTO tag_closure (ancestor_id, descendant_id, depth)
SELECT c.ancestor_id AS ancestor_id,
       sqlc.arg(descendant_id) AS descendant_id,
       c.depth + 1 AS depth
FROM tag_closure c WHERE c.descendant_id = sqlc.arg(parent_id);

-- Every tag is its own ancestor at depth zero, so a root tag still matches.
-- name: AddTagSelfClosure :exec
INSERT INTO tag_closure (ancestor_id, descendant_id, depth)
VALUES (?, ?, 0)
ON CONFLICT (ancestor_id, descendant_id) DO NOTHING;

-- name: AssignTag :exec
INSERT INTO tag_assignment (tag_id, subject_type, subject_id, source)
VALUES (?, ?, ?, ?)
ON CONFLICT (tag_id, subject_type, subject_id, source) DO NOTHING;

-- A provider refresh removes only what that provider assigned, never a user's
-- own curation (ADR-33).
-- name: ClearProviderTagAssignments :exec
DELETE FROM tag_assignment
WHERE subject_type = ? AND subject_id = ? AND source = ?;

-- name: ListTagsForSubject :many
SELECT tag_id, subject_type, subject_id, source
FROM tag_assignment WHERE subject_type = ? AND subject_id = ? ORDER BY tag_id, source;

-- The work detail page's own need, same reasoning as ListCreditsForWorkWithPerson.
-- name: ListTagsForSubjectWithName :many
SELECT t.id AS tag_id, t.name AS tag_name
FROM tag_assignment a
JOIN tag t ON t.id = a.tag_id
WHERE a.subject_type = ? AND a.subject_id = ?
ORDER BY t.name;

-- "This tag or any descendant" is one join because the closure is materialised.
-- name: ListSubjectsForTagTree :many
SELECT DISTINCT a.subject_type, a.subject_id
FROM tag_assignment a
JOIN tag_closure c ON c.descendant_id = a.tag_id
WHERE c.ancestor_id = sqlc.arg(tag_id)
  AND (sqlc.arg(subject_type) = '' OR a.subject_type = sqlc.arg(subject_type))
ORDER BY a.subject_type, a.subject_id
LIMIT sqlc.arg(page_size);

-- name: CreateSubscription :one
INSERT INTO subscription (subject_type, subject_id, media_types_json, roles_json,
                          category_id, profile_id, filters_json,
                          monitor_back_catalogue, enabled, user_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, subject_type, subject_id, media_types_json, roles_json, category_id,
          profile_id, filters_json, monitor_back_catalogue, enabled, user_id, created_at;

-- name: ListEnabledSubscriptions :many
SELECT id, subject_type, subject_id, media_types_json, roles_json, category_id,
       profile_id, filters_json, monitor_back_catalogue, enabled, user_id, created_at
FROM subscription WHERE enabled = 1 ORDER BY id;

-- name: DeleteSubscription :exec
DELETE FROM subscription WHERE id = ?;

-- name: AddRelationship :exec
INSERT INTO relationship (from_type, from_id, to_type, to_id, kind)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (from_type, from_id, to_type, to_id, kind) DO NOTHING;

-- name: ListRelationshipsFrom :many
SELECT id, from_type, from_id, to_type, to_id, kind
FROM relationship WHERE from_type = ? AND from_id = ? ORDER BY kind, to_id;

-- name: DeletePerson :exec
DELETE FROM person WHERE id = ?;
