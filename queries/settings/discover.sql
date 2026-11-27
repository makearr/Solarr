-- Discover settings per library (recommendations). A library without a row has the defaults and Discover off.

-- name: ListCategoryDiscover :many
SELECT category_id, enabled, total_amount, classic_age_years, min_rating, min_votes, auto_add_per_day, auto_add, auto_add_search,
       original_languages, languages_set, exclude_genres, exclude_networks, disabled_kinds
FROM category_discover ORDER BY category_id;

-- name: UpsertCategoryDiscover :exec
INSERT INTO category_discover (category_id, enabled, total_amount, classic_age_years, min_rating, min_votes, auto_add_per_day, auto_add, auto_add_search,
                               original_languages, languages_set, exclude_genres, exclude_networks, disabled_kinds)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (category_id) DO UPDATE SET
  enabled = excluded.enabled, total_amount = excluded.total_amount, classic_age_years = excluded.classic_age_years,
  min_rating = excluded.min_rating, min_votes = excluded.min_votes, auto_add_per_day = excluded.auto_add_per_day,
  auto_add = excluded.auto_add, auto_add_search = excluded.auto_add_search, original_languages = excluded.original_languages, languages_set = excluded.languages_set,
  exclude_genres = excluded.exclude_genres, exclude_networks = excluded.exclude_networks, disabled_kinds = excluded.disabled_kinds;

-- name: ListCategoryDiscoverTypes :many
SELECT category_id, type, enabled, amount FROM category_discover_type ORDER BY category_id, type;

-- name: DeleteCategoryDiscoverTypes :exec
DELETE FROM category_discover_type WHERE category_id = ?;

-- name: AddCategoryDiscoverType :exec
INSERT INTO category_discover_type (category_id, type, enabled, amount) VALUES (?, ?, ?, ?);
