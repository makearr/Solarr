-- Columns are listed explicitly rather than with *, so adding one cannot
-- silently reshape an existing result (documentation/sql-and-sqlc.md).

-- name: GetUserByUsername :one
SELECT id, username, password_hash, role, created_at, ui_tier, ui_tier_default, ui_tier_max,
  new_library_permission, auto_approve
FROM app_user WHERE username = ?;

-- name: ListUsers :many
SELECT id, username, password_hash, role, created_at, ui_tier, ui_tier_default, ui_tier_max,
  new_library_permission, auto_approve
FROM app_user ORDER BY username;

-- name: CreateUser :one
INSERT INTO app_user (username, password_hash, role, created_at, ui_tier, ui_tier_default, ui_tier_max,
  new_library_permission, auto_approve)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, username, password_hash, role, created_at, ui_tier, ui_tier_default, ui_tier_max,
  new_library_permission, auto_approve;

-- name: SetUserPasswordHash :exec
UPDATE app_user SET password_hash = ? WHERE id = ?;

-- name: SetUserRole :exec
UPDATE app_user SET role = ? WHERE id = ?;

-- name: SetUserTier :exec
UPDATE app_user SET ui_tier = ? WHERE id = ?;

-- name: SetUserLimits :exec
UPDATE app_user SET ui_tier_default = ?, ui_tier_max = ?, new_library_permission = ?, auto_approve = ? WHERE id = ?;

-- name: DeleteUser :exec
DELETE FROM app_user WHERE id = ?;

-- name: ListAllCategoryAccess :many
SELECT user_id, category_id, permission FROM user_category_access;

-- name: SetCategoryAccess :exec
INSERT INTO user_category_access (user_id, category_id, permission)
VALUES (?, ?, ?)
ON CONFLICT (user_id, category_id) DO UPDATE SET permission = excluded.permission;

-- name: RevokeCategoryAccess :exec
DELETE FROM user_category_access WHERE user_id = ? AND category_id = ?;

-- name: ListPreferencesForUser :many
SELECT user_id, key, value_json FROM user_preference WHERE user_id = ?;

-- name: GetPreference :one
SELECT user_id, key, value_json FROM user_preference WHERE user_id = ? AND key = ?;

-- name: SetPreference :exec
INSERT INTO user_preference (user_id, key, value_json)
VALUES (?, ?, ?)
ON CONFLICT (user_id, key) DO UPDATE SET value_json = excluded.value_json;

-- name: ListAPIKeysForUser :many
SELECT id, user_id, name, key_hash, created_at, last_used_at, key_sealed
FROM api_key WHERE user_id = ? ORDER BY created_at;

-- name: CreateAPIKey :one
INSERT INTO api_key (user_id, name, key_hash, created_at, key_sealed)
VALUES (?, ?, ?, ?, ?)
RETURNING id, user_id, name, key_hash, created_at, last_used_at, key_sealed;

-- name: TouchAPIKey :exec
UPDATE api_key SET last_used_at = ? WHERE id = ?;

-- name: DeleteAPIKey :exec
DELETE FROM api_key WHERE id = ?;

-- name: GetUserByAPIKeyHash :one
SELECT u.id, u.username, u.password_hash, u.role, u.created_at, u.ui_tier, u.ui_tier_default,
  u.ui_tier_max, u.new_library_permission, u.auto_approve, k.id AS key_id
FROM api_key k JOIN app_user u ON u.id = k.user_id
WHERE k.key_hash = ?;

-- name: ListAllUserBlockedTags :many
SELECT user_id, tag_id, tag_name FROM user_blocked_tag ORDER BY user_id, tag_name COLLATE NOCASE;

-- name: DeleteUserBlockedTags :exec
DELETE FROM user_blocked_tag WHERE user_id = ?;

-- name: InsertUserBlockedTag :exec
INSERT INTO user_blocked_tag (user_id, tag_id, tag_name) VALUES (?, ?, ?) ON CONFLICT (user_id, tag_id) DO NOTHING;
