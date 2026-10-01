-- The artists credited on a library's albums, with what a merge needs to rank them. shared is set
-- when the person also has work in another library, which a merge in this one must leave alone.
-- name: ListMergeCandidatesForCategory :many
SELECT p.id, p.name,
       CAST(EXISTS (SELECT 1 FROM person_external_id x
                    WHERE x.person_id = p.id AND x.provider NOT LIKE 'local:%') AS INTEGER) AS identified,
       CAST(COUNT(DISTINCT c.work_id) AS INTEGER) AS works,
       CAST(EXISTS (SELECT 1 FROM credit c2 JOIN desired_item d2 ON d2.work_id = c2.work_id
                    WHERE c2.person_id = p.id AND d2.category_id <> sqlc.arg(category_id)) AS INTEGER) AS shared
FROM person p
JOIN credit c ON c.person_id = p.id AND c.role = 'artist' AND c.edition_id = 0 AND c.part_id = 0
JOIN desired_item di ON di.work_id = c.work_id AND di.category_id = sqlc.arg(category_id)
GROUP BY p.id, p.name
ORDER BY p.id;

-- name: ListAliasesForPeople :many
SELECT person_id, alias FROM person_alias WHERE person_id IN (sqlc.slice(person_ids)) ORDER BY person_id, alias;

-- name: MergeCreditsInto :exec
UPDATE OR IGNORE credit SET person_id = sqlc.arg(winner_id) WHERE person_id = sqlc.arg(loser_id);

-- name: MergeExternalIDsInto :exec
UPDATE OR IGNORE person_external_id SET person_id = sqlc.arg(winner_id) WHERE person_id = sqlc.arg(loser_id);

-- name: MergeAliasesInto :exec
UPDATE OR IGNORE person_alias SET person_id = sqlc.arg(winner_id) WHERE person_id = sqlc.arg(loser_id);

-- name: MergePrimaryArtistInto :exec
UPDATE work SET primary_artist_id = sqlc.arg(winner_id),
  primary_artist_sort_name = (SELECT sort_name FROM person WHERE id = sqlc.arg(winner_id))
WHERE primary_artist_id = sqlc.arg(loser_id);

-- name: MergeTagAssignmentsInto :exec
UPDATE OR IGNORE tag_assignment SET subject_id = sqlc.arg(winner_id)
WHERE subject_type = 'person' AND subject_id = sqlc.arg(loser_id);

-- name: MergeSubscriptionsInto :exec
UPDATE OR IGNORE subscription SET subject_id = sqlc.arg(winner_id)
WHERE subject_type = 'person' AND subject_id = sqlc.arg(loser_id);

-- name: MergeArtworkInto :exec
UPDATE OR IGNORE artwork SET subject_id = sqlc.arg(winner_id)
WHERE subject_type = 'person' AND subject_id = sqlc.arg(loser_id);

-- What the winner already had stays; the loser's duplicates go with it.
-- name: DeletePersonTagAssignments :exec
DELETE FROM tag_assignment WHERE subject_type = 'person' AND subject_id = ?;

-- name: DeletePersonSubscriptions :exec
DELETE FROM subscription WHERE subject_type = 'person' AND subject_id = ?;

-- name: DeletePersonArtwork :exec
DELETE FROM artwork WHERE subject_type = 'person' AND subject_id = ?;

-- name: MergeMonitoredInto :exec
UPDATE person SET monitored = MAX(monitored, (SELECT l.monitored FROM person l WHERE l.id = sqlc.arg(loser_id)))
WHERE person.id = sqlc.arg(winner_id);
