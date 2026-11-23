-- Discover's pool, feed state, dismissals and auto-add record. Column lists follow table order.

-- name: UpsertRecommendation :one
INSERT INTO recommendation (category_id, kind, provider, external_id, title, subtitle, overview, poster_url, year, released_on, popularity,
                            rating, votes, original_language, certification, networks_json, work_id, fetched_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (category_id, kind, provider, external_id) DO UPDATE SET
  title = excluded.title, subtitle = excluded.subtitle, overview = excluded.overview, poster_url = excluded.poster_url, year = excluded.year,
  released_on = excluded.released_on, popularity = excluded.popularity, rating = excluded.rating, votes = excluded.votes,
  original_language = excluded.original_language, certification = excluded.certification, networks_json = excluded.networks_json,
  work_id = excluded.work_id, fetched_at = excluded.fetched_at
RETURNING id;

-- name: DeleteRecommendationGenres :exec
DELETE FROM recommendation_genre WHERE recommendation_id = ?;

-- name: AddRecommendationGenre :exec
INSERT OR IGNORE INTO recommendation_genre (recommendation_id, genre) VALUES (?, ?);

-- name: UpsertRecommendationFeedItem :exec
INSERT INTO recommendation_feed_item (recommendation_id, type, rank) VALUES (?, ?, ?)
ON CONFLICT (recommendation_id, type) DO UPDATE SET rank = excluded.rank;

-- What a refresh no longer names: this feed's items outside the new set.
-- name: DeleteStaleRecommendationFeedItems :exec
DELETE FROM recommendation_feed_item
WHERE type = sqlc.arg(type)
  AND recommendation_id IN (SELECT r.id FROM recommendation r WHERE r.category_id = sqlc.arg(category_id) AND r.kind = sqlc.arg(kind))
  AND recommendation_id NOT IN (SELECT value FROM json_each(sqlc.arg(keep_ids)));

-- name: DeleteOrphanRecommendations :exec
DELETE FROM recommendation
WHERE category_id = ? AND kind = ?
  AND NOT EXISTS (SELECT 1 FROM recommendation_feed_item f WHERE f.recommendation_id = recommendation.id);

-- name: GetRecommendationFeed :one
SELECT category_id, kind, type, pages_read, exhausted, refreshed_at, last_error FROM recommendation_feed
WHERE category_id = ? AND kind = ? AND type = ?;

-- name: ListRecommendationFeeds :many
SELECT category_id, kind, type, pages_read, exhausted, refreshed_at, last_error FROM recommendation_feed
WHERE category_id = ? ORDER BY kind, type;

-- name: SetRecommendationFeed :exec
INSERT INTO recommendation_feed (category_id, kind, type, pages_read, exhausted, refreshed_at, last_error) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (category_id, kind, type) DO UPDATE SET
  pages_read = excluded.pages_read, exhausted = excluded.exhausted, refreshed_at = excluded.refreshed_at, last_error = excluded.last_error;

-- What the person has not dismissed and is not already monitored; keyset on (popularity, id), no OFFSET.
-- name: ListRecommendations :many
SELECT r.id, r.category_id, r.kind, r.provider, r.external_id, r.title, r.subtitle, r.overview, r.poster_url, r.year, r.released_on, r.popularity,
       r.rating, r.votes, r.original_language, r.certification, r.networks_json, r.work_id, r.fetched_at
FROM recommendation r
WHERE r.category_id IN (SELECT value FROM json_each(sqlc.arg(category_ids)))
  AND (sqlc.arg(kind) = '' OR r.kind = sqlc.arg(kind))
  AND (sqlc.arg(type) = '' OR EXISTS (SELECT 1 FROM recommendation_feed_item f WHERE f.recommendation_id = r.id AND f.type = sqlc.arg(type)))
  AND (sqlc.arg(genre) = '' OR EXISTS (SELECT 1 FROM recommendation_genre g WHERE g.recommendation_id = r.id AND g.genre = sqlc.arg(genre)))
  AND (sqlc.arg(title) = '' OR instr(lower(r.title), lower(sqlc.arg(title))) > 0)
  AND (sqlc.arg(has_cursor) = 0 OR r.popularity < sqlc.arg(after_popularity) OR (r.popularity = sqlc.arg(after_popularity) AND r.id < sqlc.arg(after_id)))
  AND NOT EXISTS (SELECT 1 FROM recommendation_dismissal d
                  WHERE d.user_id = sqlc.arg(user_id) AND d.provider = r.provider AND d.external_id = r.external_id AND d.kind = r.kind)
  AND NOT EXISTS (SELECT 1 FROM work_external_id e JOIN desired_item di ON di.work_id = e.work_id
                  WHERE e.provider = r.provider AND e.external_id = r.external_id AND di.monitored = 1)
ORDER BY r.popularity DESC, r.id DESC
LIMIT sqlc.arg(page_size);

-- name: ListRecommendationGenresOf :many
SELECT recommendation_id, genre FROM recommendation_genre WHERE recommendation_id IN (SELECT value FROM json_each(sqlc.arg(ids))) ORDER BY recommendation_id, genre;

-- name: ListRecommendationFeedTypesOf :many
SELECT recommendation_id, type FROM recommendation_feed_item WHERE recommendation_id IN (SELECT value FROM json_each(sqlc.arg(ids))) ORDER BY recommendation_id, type;

-- The genre facet of a library and kind.
-- name: ListRecommendationGenreCounts :many
SELECT g.genre, CAST(COUNT(*) AS INTEGER) AS items
FROM recommendation_genre g JOIN recommendation r ON r.id = g.recommendation_id
WHERE r.category_id = ? AND r.kind = ?
GROUP BY g.genre ORDER BY items DESC, g.genre;

-- name: DismissRecommendation :exec
INSERT OR IGNORE INTO recommendation_dismissal (user_id, media_type, kind, provider, external_id, title, created_at) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: RestoreRecommendation :exec
DELETE FROM recommendation_dismissal WHERE user_id = ? AND provider = ? AND external_id = ? AND kind = ?;

-- name: ListRecommendationDismissals :many
SELECT user_id, media_type, kind, provider, external_id, title, created_at FROM recommendation_dismissal WHERE user_id = ? ORDER BY created_at DESC, external_id;

-- name: RecordRecommendationAutoAdd :exec
INSERT OR IGNORE INTO recommendation_auto_add (category_id, kind, provider, external_id, title, type, rating, votes, work_id, added_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: CountRecommendationAutoAddsSince :one
SELECT CAST(COUNT(*) AS INTEGER) FROM recommendation_auto_add WHERE category_id = ? AND added_at >= ?;

-- name: WasRecommendationAutoAdded :one
SELECT CAST(COUNT(*) AS INTEGER) FROM recommendation_auto_add WHERE category_id = ? AND kind = ? AND provider = ? AND external_id = ?;

-- name: DeleteRecommendationsOfCategory :exec
DELETE FROM recommendation WHERE category_id = ?;

-- name: DeleteRecommendationFeedsOfCategory :exec
DELETE FROM recommendation_feed WHERE category_id = ?;

-- name: DeleteRecommendationAutoAddsOfCategory :exec
DELETE FROM recommendation_auto_add WHERE category_id = ?;

-- A work added by any path leaves the pool at once.
-- name: DeleteRecommendationsByExternalID :exec
DELETE FROM recommendation WHERE provider = ? AND external_id = ?;

-- The work a provider id names and whether any of its items is monitored.
-- name: GetHeldWorkByExternalID :one
SELECT e.work_id, CAST(EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = e.work_id AND d.monitored = 1) AS INTEGER) AS monitored
FROM work_external_id e WHERE e.provider = ? AND e.external_id = ?;

-- Members of a collection the library already holds one of, not monitored yet, named by the provider that names the collection.
-- name: ListCollectionRecommendationCandidates :many
SELECT c.name AS collection_name, w.id AS work_id, w.canonical_title, CAST(COALESCE(w.year, 0) AS INTEGER) AS year,
       CAST(COALESCE(w.description, '') AS TEXT) AS description,
       CAST(COALESCE(w.original_language, '') AS TEXT) AS original_language, e.provider, e.external_id
FROM collection c
JOIN collection_external_id ce ON ce.collection_id = c.id
JOIN collection_member m ON m.collection_id = c.id
JOIN work w ON w.id = m.work_id AND w.media_type = sqlc.arg(media_type)
JOIN work_external_id e ON e.work_id = w.id AND e.provider = ce.provider
WHERE EXISTS (SELECT 1 FROM collection_member held
              JOIN desired_item d ON d.work_id = held.work_id
              WHERE held.collection_id = c.id AND d.category_id = sqlc.arg(category_id) AND d.state IN ('COMPLETE', 'UPGRADABLE'))
  AND NOT EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = w.id AND d.monitored = 1)
ORDER BY c.name, m.ord, w.id
LIMIT sqlc.arg(page_size);

-- name: GetRecommendation :one
SELECT id, category_id, kind, provider, external_id, title, subtitle, overview, poster_url, year, released_on, popularity,
       rating, votes, original_language, certification, networks_json, work_id, fetched_at
FROM recommendation WHERE id = ?;
