-- Media server connections. Column lists follow table order.

-- name: CreateMediaServer :one
INSERT INTO media_server (kind, name, base_url, username, credential, enabled, as_metadata, push_files, announce,
                          sync_playback, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, kind, name, base_url, username, credential, enabled, as_metadata, push_files, announce,
          sync_playback, created_at, updated_at, last_ok_at, last_error;

-- name: GetMediaServer :one
SELECT id, kind, name, base_url, username, credential, enabled, as_metadata, push_files, announce,
       sync_playback, created_at, updated_at, last_ok_at, last_error
FROM media_server WHERE id = ?;

-- name: ListMediaServers :many
SELECT id, kind, name, base_url, username, credential, enabled, as_metadata, push_files, announce,
       sync_playback, created_at, updated_at, last_ok_at, last_error
FROM media_server ORDER BY kind, name, id;

-- name: UpdateMediaServer :exec
UPDATE media_server
SET name = ?, base_url = ?, username = ?, enabled = ?, as_metadata = ?, push_files = ?, announce = ?,
    sync_playback = ?, updated_at = ?
WHERE id = ?;

-- A separate statement so an edit that keeps the credential never reseals it (a fresh nonce every call).
-- name: SetMediaServerCredential :exec
UPDATE media_server SET credential = ?, updated_at = ? WHERE id = ?;

-- name: SetMediaServerResult :exec
UPDATE media_server SET last_ok_at = ?, last_error = ? WHERE id = ?;

-- name: DeleteMediaServer :exec
DELETE FROM media_server WHERE id = ?;

-- name: ListMediaServerLibraries :many
SELECT server_id, category_id, server_library_id, server_library
FROM media_server_library WHERE server_id = ? ORDER BY category_id, server_library_id;

-- name: ListAllMediaServerLibraries :many
SELECT server_id, category_id, server_library_id, server_library
FROM media_server_library ORDER BY server_id, category_id, server_library_id;

-- name: DeleteMediaServerLibraries :exec
DELETE FROM media_server_library WHERE server_id = ?;

-- name: AddMediaServerLibrary :exec
INSERT INTO media_server_library (server_id, category_id, server_library_id, server_library) VALUES (?, ?, ?, ?);

-- name: ListMediaServerPaths :many
SELECT server_id, solarr_prefix, server_prefix FROM media_server_path WHERE server_id = ? ORDER BY solarr_prefix;

-- name: DeleteMediaServerPaths :exec
DELETE FROM media_server_path WHERE server_id = ?;

-- name: AddMediaServerPath :exec
INSERT INTO media_server_path (server_id, solarr_prefix, server_prefix) VALUES (?, ?, ?);

-- name: ListMediaServerUsers :many
SELECT server_id, server_user_id, name, user_id, counted FROM media_server_user WHERE server_id = ? ORDER BY name, server_user_id;

-- name: DeleteMediaServerUsers :exec
DELETE FROM media_server_user WHERE server_id = ?;

-- name: AddMediaServerUser :exec
INSERT INTO media_server_user (server_id, server_user_id, name, user_id, counted) VALUES (?, ?, ?, ?, ?);
