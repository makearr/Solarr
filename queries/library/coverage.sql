-- The parts of a work that a held collected edition stands for, with the edition's work and title.
-- name: ListHeldCoverageOfWork :many
SELECT p.media_ref AS covered_ref, q.work_id AS edition_work_id, w.canonical_title AS edition_title
FROM part_coverage cov
JOIN work_part p ON p.id = cov.covered_part_id
JOIN work_part q ON q.id = cov.covering_part_id
JOIN work w ON w.id = q.work_id
WHERE p.work_id = ?
  AND EXISTS (SELECT 1 FROM desired_item_file f
              JOIN desired_item d ON d.id = f.desired_item_id
              WHERE d.work_id = q.work_id AND f.role = 'primary' AND f.media_ref = q.media_ref
                AND NOT (EXISTS (SELECT 1 FROM library_path gone WHERE gone.media_object_id = f.media_object_id AND gone.missing_since IS NOT NULL)
                         AND NOT EXISTS (SELECT 1 FROM library_path held WHERE held.media_object_id = f.media_object_id AND held.missing_since IS NULL)))
ORDER BY p.sort_key, p.id;

-- What an edition's parts cover, in the covered series' words.
-- name: ListCoverageByEdition :many
SELECT cov.covering_part_id, cov.covered_part_id, cov.source, p.work_id AS covered_work_id, p.media_ref AS covered_ref
FROM part_coverage cov
JOIN work_part q ON q.id = cov.covering_part_id
JOIN work_part p ON p.id = cov.covered_part_id
WHERE q.work_id = ?
ORDER BY p.work_id, p.sort_key, p.id;

-- name: ReplaceCoverageSource :exec
DELETE FROM part_coverage WHERE covering_part_id = ? AND source = ?;

-- name: AddCoverage :exec
INSERT OR IGNORE INTO part_coverage (covering_part_id, covered_part_id, source) VALUES (?, ?, ?);
