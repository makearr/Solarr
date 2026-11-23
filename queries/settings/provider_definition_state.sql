-- What a person set about a provider definition file. Column lists follow table order.

-- name: ListProviderDefinitionStates :many
SELECT kind, key, enabled, approved_hash, approved_hosts, settings, updated_at
FROM provider_definition_state ORDER BY kind, key;

-- name: GetProviderDefinitionState :one
SELECT kind, key, enabled, approved_hash, approved_hosts, settings, updated_at
FROM provider_definition_state WHERE kind = ? AND key = ?;

-- name: SetProviderDefinitionEnabled :exec
INSERT INTO provider_definition_state (kind, key, enabled, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT (kind, key) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at;

-- name: ApproveProviderDefinition :exec
INSERT INTO provider_definition_state (kind, key, approved_hash, approved_hosts, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (kind, key) DO UPDATE SET approved_hash = excluded.approved_hash, approved_hosts = excluded.approved_hosts,
  updated_at = excluded.updated_at;

-- name: SetProviderDefinitionSettings :exec
INSERT INTO provider_definition_state (kind, key, settings, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT (kind, key) DO UPDATE SET settings = excluded.settings, updated_at = excluded.updated_at;

-- name: DeleteProviderDefinitionState :exec
DELETE FROM provider_definition_state WHERE kind = ? AND key = ?;
