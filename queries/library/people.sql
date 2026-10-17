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

-- Fills in only the person columns still empty, never overwriting one that
-- already holds something -- a backfill for people resolved before a field
-- had a source, not a refresh (a manual override must always win, ADR-8).
-- name: BackfillPersonDetails :exec
UPDATE person SET
  name           = COALESCE(NULLIF(CAST(sqlc.arg(name) AS TEXT), ''), name),
  sort_name      = COALESCE(NULLIF(CAST(sqlc.arg(sort_name) AS TEXT), ''), sort_name),
  disambiguation = CASE WHEN COALESCE(disambiguation, '') = '' THEN sqlc.arg(disambiguation) ELSE disambiguation END,
  kind           = CASE WHEN COALESCE(kind, '') = ''           THEN sqlc.arg(kind)           ELSE kind END,
  gender         = CASE WHEN COALESCE(gender, '') = ''         THEN sqlc.arg(gender)         ELSE gender END,
  country        = CASE WHEN COALESCE(country, '') = ''        THEN sqlc.arg(country)        ELSE country END,
  born           = CASE WHEN COALESCE(born, '') = ''           THEN sqlc.arg(born)           ELSE born END,
  died           = CASE WHEN COALESCE(died, '') = ''           THEN sqlc.arg(died)           ELSE died END,
  fields_json    = CASE WHEN COALESCE(fields_json, '') = ''    THEN sqlc.arg(fields_json)    ELSE fields_json END
WHERE id = sqlc.arg(id);

-- name: AddPersonAlias :exec
INSERT INTO person_alias (person_id, alias, normalized, source)
VALUES (?, ?, ?, ?)
ON CONFLICT (person_id, alias) DO UPDATE SET
  normalized = excluded.normalized,
  source = excluded.source;

-- The person detail page's own need: every alias this person is known by.
-- name: ListAliasesForPerson :many
SELECT alias FROM person_alias WHERE person_id = ? ORDER BY alias;

-- A stage name is often the only way to connect a release to a person (ADR-32).
-- name: FindPeopleByAlias :many
SELECT p.id, p.name, p.sort_name, p.disambiguation, p.kind, p.gender, p.country,
       p.born, p.died, p.fields_json, p.monitored, p.added_at
FROM person p
JOIN person_alias a ON a.person_id = p.id
WHERE a.normalized = ? ORDER BY p.sort_name COLLATE NOCASE, p.id;

-- A provider-identified person by this name, ignoring case: an unverified album's folder artist joins them.
-- name: FindIdentifiedPeopleByName :many
SELECT p.id FROM person p
WHERE p.name = sqlc.arg(name) COLLATE NOCASE
  AND EXISTS (SELECT 1 FROM person_external_id x WHERE x.person_id = p.id AND x.provider NOT LIKE 'local:%')
ORDER BY p.id LIMIT 2;

-- The same by a MusicBrainz alias: an artist renamed upstream is still found by its old folder name.
-- name: FindIdentifiedPeopleByAlias :many
SELECT DISTINCT p.id FROM person p
JOIN person_alias a ON a.person_id = p.id
WHERE a.alias = sqlc.arg(name) COLLATE NOCASE
  AND EXISTS (SELECT 1 FROM person_external_id x WHERE x.person_id = p.id AND x.provider NOT LIKE 'local:%')
ORDER BY p.id LIMIT 2;

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

-- What subscription.expand needs to go from a subscribed person back to the
-- provider id their discography is browsed by.
-- name: ListPersonExternalIDs :many
SELECT provider, external_id, person_id
FROM person_external_id WHERE person_id = ? ORDER BY provider;

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
SELECT c.person_id, c.role, c.ord, p.name AS person_name, c.character,
       CAST(COALESCE(json_extract(p.fields_json, '$.image_url'), '') AS TEXT) AS image_url
FROM credit c
JOIN person p ON p.id = c.person_id
WHERE c.work_id = ? AND c.edition_id = 0 AND c.part_id = 0
ORDER BY c.role, c.ord, p.sort_name COLLATE NOCASE;

-- The credits of a page of parts: an episode's guest stars, director and writers.
-- name: ListCreditsForParts :many
SELECT c.part_id, c.person_id, c.role, c.character, p.name AS person_name
FROM credit c
JOIN person p ON p.id = c.person_id
WHERE c.work_id = sqlc.arg(work_id) AND c.part_id IN (sqlc.slice('part_ids'))
ORDER BY c.part_id, c.ord, p.sort_name COLLATE NOCASE, p.id;

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

-- Discography rows, plus availability (wanted state, on-disk), are composed
-- as raw SQL in internal/db/library/people.go instead of here: the scalar
-- subqueries that carry wanted_state/on_disk are correlated per row, which
-- sqlc cannot see is nullable the way it can a real table column -- it would
-- type wanted_state as a plain non-null string and panic the first row with
-- no monitored desired_item. works.go's workStateColumn already established
-- the same raw-SQL-with-manual-Scan shape for exactly this reason.

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
ORDER BY o.sort_name COLLATE NOCASE, o.id;

-- name: LinkWorkOrganisation :exec
INSERT INTO work_organisation (work_id, organisation_id, role)
VALUES (?, ?, ?)
ON CONFLICT (work_id, organisation_id, role) DO NOTHING;

-- name: ListOrganisationsForWork :many
SELECT work_id, organisation_id, role
FROM work_organisation WHERE work_id = ? ORDER BY role, organisation_id;

-- The organisations a work is made or aired by, named.
-- name: ListNamedOrganisationsForWork :many
SELECT wo.organisation_id, o.name, wo.role
FROM work_organisation wo JOIN organisation o ON o.id = wo.organisation_id
WHERE wo.work_id = ? ORDER BY wo.role, o.name COLLATE NOCASE, o.id;

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

-- Genre chips for the artist page (Lidarr-like): the union of every genre
-- tagged on any work credited to this person -- Person itself carries no
-- genre of its own (ADR-32's own fields are biographical, not stylistic).
-- name: ListGenresForPersonDiscography :many
SELECT DISTINCT t.name AS tag_name
FROM tag_assignment a
JOIN tag t ON t.id = a.tag_id
JOIN credit c ON c.work_id = a.subject_id
WHERE a.subject_type = 'work' AND c.person_id = sqlc.arg(person_id)
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

-- One follow per subject, user and library; "IS" matches NULL as the constraint's own NULLs do not.
-- name: GetSubscriptionForSubject :one
SELECT id, subject_type, subject_id, media_types_json, roles_json, category_id,
       profile_id, filters_json, monitor_back_catalogue, enabled, user_id, created_at
FROM subscription WHERE subject_type = ? AND subject_id = ? AND user_id IS ? AND category_id IS ?
ORDER BY id LIMIT 1;

-- name: ListSubscriptionsForSubject :many
SELECT id, subject_type, subject_id, media_types_json, roles_json, category_id,
       profile_id, filters_json, monitor_back_catalogue, enabled, user_id, created_at
FROM subscription WHERE subject_type = ? AND subject_id = ? AND user_id IS ?
ORDER BY category_id, id;

-- name: CountEnabledSubscriptionsForSubject :one
SELECT CAST(COUNT(*) AS INTEGER) AS follows FROM subscription WHERE subject_type = ? AND subject_id = ? AND enabled = 1;

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

-- name: RecordPersonRefresh :exec
INSERT INTO person_refresh (person_id, refreshed_at) VALUES (?, ?)
ON CONFLICT (person_id) DO UPDATE SET refreshed_at = excluded.refreshed_at;

-- The monitored artists to refresh next: never refreshed first, then the stalest.
-- name: ListArtistsDueRefresh :many
SELECT p.id FROM person p
LEFT JOIN person_refresh r ON r.person_id = p.id
WHERE p.monitored = 1 AND (r.refreshed_at IS NULL OR r.refreshed_at < sqlc.arg(before))
ORDER BY COALESCE(r.refreshed_at, 0), p.id
LIMIT sqlc.arg(page_size);

-- name: UpdateSubscriptionScope :exec
UPDATE subscription SET media_types_json = ?, roles_json = ?, category_id = ?, filters_json = ?,
                        monitor_back_catalogue = ?, enabled = 1
WHERE id = ?;

-- name: ListOrganisationExternalIDs :many
SELECT provider, external_id FROM organisation_external_id WHERE organisation_id = ? ORDER BY provider, external_id;

-- name: SetOrganisationMonitored :exec
UPDATE organisation SET monitored = ? WHERE id = ?;

-- A work is listed under the first person credited in the role: a movie's director.
-- name: SetWorkLeadFromRole :exec
UPDATE work SET
  lead_person_id = (SELECT cr.person_id FROM credit cr WHERE cr.work_id = sqlc.arg(work_id) AND cr.part_id = 0 AND cr.edition_id = 0 AND cr.role = sqlc.arg(role)
                    ORDER BY cr.ord, cr.person_id LIMIT 1),
  lead_person_sort_name = (SELECT pe.sort_name FROM credit cr JOIN person pe ON pe.id = cr.person_id
                           WHERE cr.work_id = sqlc.arg(work_id) AND cr.part_id = 0 AND cr.edition_id = 0 AND cr.role = sqlc.arg(role)
                           ORDER BY cr.ord, cr.person_id LIMIT 1)
WHERE work.id = sqlc.arg(work_id)
  AND EXISTS (SELECT 1 FROM credit cr WHERE cr.work_id = sqlc.arg(work_id) AND cr.part_id = 0 AND cr.edition_id = 0 AND cr.role = sqlc.arg(role));
