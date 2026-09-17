-- work_root_folder records on-disk presence: which root folder a scan found a work
-- under. It is no longer the sole ADR-27 visibility signal (desired_item.category_id
-- covers a work with no root folder yet, e.g. subscription-discovered), but it is
-- still real and still needed for that half.

-- name: LinkWorkRootFolder :exec
INSERT INTO work_root_folder (work_id, root_folder_id, first_seen_at)
VALUES (?, ?, ?)
ON CONFLICT (work_id, root_folder_id) DO NOTHING;

-- name: ListRootFoldersForWork :many
SELECT root_folder_id FROM work_root_folder WHERE work_id = ? ORDER BY root_folder_id;

-- The "works" count for one category: the union of on-disk presence
-- (work_root_folder) and desired-state membership (desired_item.category_id) --
-- a subscription-discovered work has no root folder yet but still belongs to its
-- category (private/next.md's work_root_folder retirement). Distinct because a
-- scanned, monitored work has rows on both sides and must count once.
-- name: CountWorksVisibleToCategory :one
SELECT CAST(COUNT(DISTINCT work_id) AS INTEGER) AS total FROM (
  SELECT work_id FROM work_root_folder
  WHERE root_folder_id IN (SELECT value FROM json_each(sqlc.arg(root_folder_ids)))
  UNION
  SELECT work_id FROM desired_item WHERE category_id = sqlc.arg(category_id)
);

-- The "parts" count for one category (ADR-54's work_part-entity navigation levels,
-- e.g. music's Tracks): every work_part belonging to a work visible to the
-- category, by the same on-disk-or-desired union CountWorksVisibleToCategory uses.
-- name: CountWorkPartsVisibleToCategory :one
SELECT CAST(COUNT(*) AS INTEGER) AS total FROM work_part
WHERE work_id IN (
  SELECT work_id FROM work_root_folder
  WHERE root_folder_id IN (SELECT value FROM json_each(sqlc.arg(root_folder_ids)))
  UNION
  SELECT work_id FROM desired_item WHERE category_id = sqlc.arg(category_id)
);

-- The "artists" count for one category (ADR-54's person-entity navigation levels,
-- e.g. music's Artists): distinct persons credited with role 'artist' on a work
-- visible to the category. Hard-codes the 'artist' role, the same way
-- workArtistColumn already does (internal/db/library/works.go) -- generalising which
-- role counts as a level's "primary person" waits for a second media type that
-- needs a different one.
-- name: CountArtistCreditsVisibleToCategory :one
SELECT CAST(COUNT(DISTINCT person_id) AS INTEGER) AS total FROM credit
WHERE role = 'artist' AND work_id IN (
  SELECT work_id FROM work_root_folder
  WHERE root_folder_id IN (SELECT value FROM json_each(sqlc.arg(root_folder_ids)))
  UNION
  SELECT work_id FROM desired_item WHERE category_id = sqlc.arg(category_id)
);

-- The first file identification resolved a work from wins; a later one changes
-- nothing. The row goes with its file (ON DELETE CASCADE), and the next scan
-- that resolves the work from another file records that one instead.
-- name: LinkWorkFileHint :exec
INSERT INTO work_file_hint (work_id, media_object_id, first_seen_at)
VALUES (?, ?, ?)
ON CONFLICT (work_id) DO NOTHING;
