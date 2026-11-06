-- name: UpsertCrossoverEvent :exec
INSERT INTO crossover_event (collection_id, name_key, total_parts) VALUES (?, ?, ?)
ON CONFLICT (collection_id) DO UPDATE SET name_key = excluded.name_key, total_parts = excluded.total_parts;

-- name: GetCrossoverEvent :one
SELECT collection_id, name_key, total_parts FROM crossover_event WHERE collection_id = ?;

-- name: ListCrossoverEventInfo :many
SELECT collection_id, name_key, total_parts FROM crossover_event WHERE collection_id IN (sqlc.slice(collection_ids));

-- Events a person has not edited, with the dates of their members, to merge halves a pass made apart.
-- name: ListAutoCrossovers :many
SELECT c.id, c.name, CAST(COALESCE(MIN(p.date), '') AS TEXT) AS first_date, CAST(COALESCE(MAX(p.date), '') AS TEXT) AS last_date
FROM collection c
LEFT JOIN collection_member m ON m.collection_id = c.id AND m.part_id != 0
LEFT JOIN work_part p ON p.id = m.part_id
WHERE c.kind = 'crossover' AND c.source = 'auto' AND c.media_type = ?
GROUP BY c.id, c.name
ORDER BY c.id;

-- name: SetCollectionSource :exec
UPDATE collection SET source = ? WHERE id = ?;

-- name: ClearCrossoverMemberGuesses :exec
DELETE FROM crossover_member_guess WHERE collection_id = ?;

-- name: AddCrossoverMemberGuess :exec
INSERT OR IGNORE INTO crossover_member_guess (collection_id, part_id) VALUES (?, ?);

-- name: ListCrossoverMemberGuesses :many
SELECT collection_id, part_id FROM crossover_member_guess WHERE collection_id IN (sqlc.slice(collection_ids));

-- name: UpsertCrossoverMissingPart :exec
INSERT INTO crossover_missing_part (collection_id, number, provider, external_id, series_title, season, episode, title, air_date)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (collection_id, provider, external_id, season, episode) DO UPDATE SET
  number = excluded.number, series_title = excluded.series_title, title = excluded.title, air_date = excluded.air_date;

-- name: DeleteCrossoverMissingParts :exec
DELETE FROM crossover_missing_part WHERE collection_id = ?;

-- name: DeleteCrossoverMissingPart :exec
DELETE FROM crossover_missing_part WHERE collection_id = ? AND provider = ? AND external_id = ? AND season = ? AND episode = ?;

-- name: ListCrossoverMissingParts :many
SELECT collection_id, number, provider, external_id, series_title, season, episode, title, air_date
FROM crossover_missing_part WHERE collection_id IN (sqlc.slice(collection_ids))
ORDER BY collection_id, number, air_date, series_title;

-- name: MoveCrossoverMissingParts :exec
UPDATE OR IGNORE crossover_missing_part SET collection_id = ? WHERE collection_id = ?;

-- name: AddCrossoverNotSame :exec
INSERT OR IGNORE INTO crossover_not_same (low_work_id, low_ref, high_work_id, high_ref, created_at) VALUES (?, ?, ?, ?, ?);

-- name: DeleteCrossoverNotSame :exec
DELETE FROM crossover_not_same WHERE low_work_id = ? AND low_ref = ? AND high_work_id = ? AND high_ref = ?;

-- Every pair of episodes a person said are not one broadcast, as the parts they are now.
-- name: ListCrossoverNotSameParts :many
SELECT a.id AS low_part_id, b.id AS high_part_id
FROM crossover_not_same n
JOIN work_part a ON a.work_id = n.low_work_id AND a.media_ref = n.low_ref
JOIN work_part b ON b.work_id = n.high_work_id AND b.media_ref = n.high_ref;

-- name: AddCrossoverEventDismissed :exec
INSERT OR IGNORE INTO crossover_event_dismissed (name_key, first_date) VALUES (?, ?);

-- name: ListCrossoverEventDismissed :many
SELECT name_key, first_date FROM crossover_event_dismissed;

-- name: GetCrossoverFamilyChecked :one
SELECT checked_at FROM crossover_family_checked WHERE family_key = ?;

-- name: SetCrossoverFamilyChecked :exec
INSERT INTO crossover_family_checked (family_key, checked_at) VALUES (?, ?)
ON CONFLICT (family_key) DO UPDATE SET checked_at = excluded.checked_at;

-- Every work pair that shares an event, once each, with the event it is in.
-- name: ListCrossoverEventWorks :many
SELECT DISTINCT m.collection_id, c.name, m.work_id
FROM collection_member m JOIN collection c ON c.id = m.collection_id
WHERE c.kind = 'crossover' AND c.media_type = ? AND m.part_id != 0
ORDER BY m.collection_id, m.work_id;

-- The event each episode of an event is in, to keep one episode out of two events.
-- name: ListCrossoverMemberPartOwners :many
SELECT m.part_id, m.collection_id
FROM collection_member m JOIN collection c ON c.id = m.collection_id
WHERE c.kind = 'crossover' AND c.media_type = ? AND m.part_id != 0;
