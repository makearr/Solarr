-- name: AppendHistory :one
INSERT INTO history (at, kind, category_id, work_id, acquisition_id, release_title, work_title, lead_name, indexer, client, detail)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;
