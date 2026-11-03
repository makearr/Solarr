-- One row per file a replacement kept or turned away, so "why did this file replace that one" can be answered (pain point 18).
-- name: InsertImportDecision :exec
INSERT INTO import_decision (desired_item_id, acquisition_id, incumbent_object_id, arrived_probe_json,
                             comparison_json, outcome, reason_json, decided_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListImportDecisionsForWork :many
SELECT d.id, d.desired_item_id, d.acquisition_id, d.incumbent_object_id, d.arrived_probe_json,
       d.comparison_json, d.outcome, d.reason_json, d.decided_at, d.overridden_by, d.overridden_at
FROM import_decision d
JOIN desired_item di ON di.id = d.desired_item_id
WHERE di.work_id = sqlc.arg(work_id)
ORDER BY d.decided_at DESC, d.id DESC
LIMIT sqlc.arg(page_size);
