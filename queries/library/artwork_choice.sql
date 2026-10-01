-- The ids a user's image picker asks the providers with.
-- name: GetWorkReleaseGroupMBID :one
SELECT CAST(COALESCE(MIN(external_id), '') AS TEXT) AS release_group_mbid
FROM work_external_id WHERE work_id = ? AND provider = 'musicbrainz:release-group';

-- name: ListWorkReleaseMBIDs :many
SELECT x.external_id FROM edition e
JOIN edition_external_id x ON x.edition_id = e.id AND x.provider = 'musicbrainz:release'
WHERE e.work_id = ? AND e.kind != 'unknown'
ORDER BY e.id LIMIT 6;

-- name: GetPersonArtistMBID :one
SELECT CAST(COALESCE(MIN(external_id), '') AS TEXT) AS artist_mbid
FROM person_external_id WHERE person_id = ? AND provider = 'musicbrainz:artist';
