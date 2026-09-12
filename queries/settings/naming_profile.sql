-- Section 11.8, todo 4.5's own remaining schema step (migration 0013). Read-only for now:
-- nothing writes a second naming_profile row yet, so there is no Create/Update/Delete pass
-- here -- only enough to prove the shipped default seeded correctly.

-- name: ListNamingProfiles :many
SELECT id, name, folder_template, file_template FROM naming_profile ORDER BY name;
