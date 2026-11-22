-- name: SetEditionDetail :exec
INSERT INTO edition_detail (edition_id, disambiguation, packaging, language, script)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (edition_id) DO UPDATE SET
  disambiguation = excluded.disambiguation,
  packaging = excluded.packaging,
  language = excluded.language,
  script = excluded.script;

-- name: GetEditionDetail :one
SELECT edition_id, disambiguation, packaging, language, script
FROM edition_detail WHERE edition_id = ?;

-- name: SetWorkPartDetail :exec
INSERT INTO work_part_detail (part_id, medium_title, artist_credit)
VALUES (?, ?, ?)
ON CONFLICT (part_id) DO UPDATE SET
  medium_title = excluded.medium_title,
  artist_credit = excluded.artist_credit;

-- name: GetWorkPartDetail :one
SELECT part_id, medium_title, artist_credit
FROM work_part_detail WHERE part_id = ?;

-- name: ListWorkPartDetails :many
SELECT part_id, medium_title, artist_credit
FROM work_part_detail
WHERE part_id IN (sqlc.slice('part_ids'))
ORDER BY part_id;
