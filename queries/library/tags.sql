-- The tag tree: genre trees, aliases, moving and merging tags (plan 10-libraries L1). Column lists follow table order.

-- name: GetTag :one
SELECT id, name, slug, parent_id, description, source, genre_tree FROM tag WHERE id = ?;

-- name: ListTagTree :many
SELECT t.id, t.name, t.slug, t.parent_id, t.description, t.source, t.genre_tree,
       CAST((SELECT COUNT(DISTINCT a.subject_id) FROM tag_assignment a
             WHERE a.tag_id = t.id AND a.subject_type = 'work') AS INTEGER) AS works
FROM tag t
WHERE sqlc.arg(genre_tree) = '*' OR t.genre_tree = sqlc.arg(genre_tree)
ORDER BY t.genre_tree, t.name COLLATE NOCASE, t.id;

-- name: ListTagAliases :many
SELECT tag_id, alias FROM tag_alias ORDER BY tag_id, alias;

-- The tags of one genre tree an alias names; one alias may name several ("Action & Adventure").
-- name: ListTagsByAlias :many
SELECT t.id FROM tag_alias a JOIN tag t ON t.id = a.tag_id
WHERE a.alias = sqlc.arg(alias) AND t.genre_tree = sqlc.arg(genre_tree)
ORDER BY t.id;

-- name: AddTagAlias :exec
INSERT INTO tag_alias (tag_id, alias) VALUES (?, ?) ON CONFLICT (tag_id, alias) DO NOTHING;

-- name: GetGenreTreeSeed :one
SELECT seeded_version FROM genre_tree WHERE key = ?;

-- name: SetGenreTreeSeed :exec
INSERT INTO genre_tree (key, seeded_version) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET seeded_version = excluded.seeded_version;

-- name: ListTagsOfGenreTree :many
SELECT id, name, slug, parent_id, description, source, genre_tree FROM tag WHERE genre_tree = ? ORDER BY id;

-- name: SetTagName :exec
UPDATE tag SET name = ?, slug = ? WHERE id = ?;

-- name: SetTagParentColumn :exec
UPDATE tag SET parent_id = ? WHERE id = ?;

-- Moving a subtree: first cut every path from outside the subtree into it, then join it under the new parent.
-- name: CutTagSubtree :exec
DELETE FROM tag_closure
WHERE descendant_id IN (SELECT d.descendant_id FROM tag_closure d WHERE d.ancestor_id = sqlc.arg(tag_id))
  AND ancestor_id NOT IN (SELECT d.descendant_id FROM tag_closure d WHERE d.ancestor_id = sqlc.arg(tag_id));

-- name: JoinTagSubtree :exec
INSERT INTO tag_closure (ancestor_id, descendant_id, depth)
SELECT above.ancestor_id, below.descendant_id, above.depth + below.depth + 1
FROM tag_closure above, tag_closure below
WHERE above.descendant_id = sqlc.arg(parent_id) AND below.ancestor_id = sqlc.arg(tag_id);

-- name: IsTagInSubtree :one
SELECT CAST(COUNT(*) AS INTEGER) FROM tag_closure WHERE ancestor_id = sqlc.arg(tag_id) AND descendant_id = sqlc.arg(candidate_id);

-- name: MoveTagAssignments :exec
INSERT OR IGNORE INTO tag_assignment (tag_id, subject_type, subject_id, source)
SELECT sqlc.arg(into_id), a.subject_type, a.subject_id, a.source FROM tag_assignment a WHERE a.tag_id = sqlc.arg(from_id);

-- name: MoveTagAliases :exec
INSERT OR IGNORE INTO tag_alias (tag_id, alias)
SELECT sqlc.arg(into_id), a.alias FROM tag_alias a WHERE a.tag_id = sqlc.arg(from_id);

-- name: ListTagChildren :many
SELECT id FROM tag WHERE parent_id = ? ORDER BY id;

-- name: DeleteTag :execrows
DELETE FROM tag WHERE id = ?;

-- A refresh replaces every provider's genres of a work at once: every source but a person's own.
-- name: ClearWorkProviderTagsOfGenreTree :exec
DELETE FROM tag_assignment
WHERE subject_type = 'work' AND subject_id = sqlc.arg(work_id) AND source <> 'user'
  AND tag_id IN (SELECT t.id FROM tag t WHERE t.genre_tree = sqlc.arg(genre_tree));

-- Genres stored before they were tags, a page of works at a time.
-- name: ListClassificationGenres :many
SELECT wc.work_id, w.media_type, wc.value, wc.source FROM work_classification wc JOIN work w ON w.id = wc.work_id
WHERE wc.kind = 'genre' AND wc.work_id IN (
  SELECT DISTINCT c.work_id FROM work_classification c WHERE c.kind = 'genre' ORDER BY c.work_id LIMIT sqlc.arg(page_size))
ORDER BY wc.work_id, wc.value;

-- name: ClearClassificationGenres :exec
DELETE FROM work_classification WHERE work_id = ? AND kind = 'genre';

-- A work's genres: its tags of any genre tree, whoever assigned them.
-- name: ListGenreTagsOfWork :many
SELECT DISTINCT t.name FROM tag_assignment a JOIN tag t ON t.id = a.tag_id
WHERE a.subject_type = 'work' AND a.subject_id = ? AND t.genre_tree <> ''
ORDER BY t.name;

-- name: ListTagAncestorNames :many
SELECT t.name FROM tag_closure c JOIN tag t ON t.id = c.ancestor_id WHERE c.descendant_id = ? ORDER BY c.depth;

-- The facts a library's rules read of each of its works (plan 10-libraries L5).
-- name: ListWorkTraitsInCategory :many
SELECT w.id, w.canonical_title, w.media_type, CAST(COALESCE(w.original_language, '') AS TEXT) AS language,
       CAST(COALESCE((SELECT MAX(f.value) FROM work_field f WHERE f.work_id = w.id AND f.field = 'original_country'), '') AS TEXT) AS country,
       CAST(EXISTS (SELECT 1 FROM work_external_id x WHERE x.work_id = w.id AND x.provider = 'anilist') AS INTEGER) AS anilist,
       CAST(COALESCE((SELECT MAX(wc.age) FROM work_certification wc WHERE wc.work_id = w.id AND wc.age >= 0), -1) AS INTEGER) AS rating_age
FROM work w
WHERE w.id IN (SELECT d.work_id FROM desired_item d WHERE d.category_id = ?)
ORDER BY w.sort_title, w.id;

-- name: ListWorkGenreTreesInCategory :many
SELECT DISTINCT a.subject_id AS work_id, t.name
FROM tag_assignment a
JOIN tag_closure c ON c.descendant_id = a.tag_id
JOIN tag t ON t.id = c.ancestor_id
WHERE a.subject_type = 'work' AND t.genre_tree <> ''
  AND a.subject_id IN (SELECT d.work_id FROM desired_item d WHERE d.category_id = ?);
