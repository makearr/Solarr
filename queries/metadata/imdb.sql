-- name: UpsertImdbRating :exec
INSERT INTO imdb_rating (tconst, rating, votes) VALUES (?, ?, ?)
ON CONFLICT (tconst) DO UPDATE SET rating = excluded.rating, votes = excluded.votes;

-- name: GetImdbRating :one
SELECT tconst, rating, votes FROM imdb_rating WHERE tconst = ?;

-- name: CountImdbRatings :one
SELECT CAST(COUNT(*) AS INTEGER) FROM imdb_rating;
