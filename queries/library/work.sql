-- Identity queries. Column lists follow table order, or sqlc stops reusing models.

-- name: CreateWork :one
INSERT INTO work (media_type, canonical_title, sort_title, original_language,
                  year, runtime_ms, added_at, updated_at, added_by, subtype)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, media_type, canonical_title, sort_title, original_language,
          year, runtime_ms, added_at, updated_at, added_by, description,
          lead_person_id, lead_person_sort_name, subtype;

-- name: GetWork :one
SELECT id, media_type, canonical_title, sort_title, original_language,
       year, runtime_ms, added_at, updated_at, added_by, description,
       lead_person_id, lead_person_sort_name, subtype
FROM work WHERE id = ?;

-- Resolving a provider id to a work is the first step of the Identifier chain (ADR-24).
-- name: GetWorkByExternalID :one
SELECT w.id, w.media_type, w.canonical_title, w.sort_title, w.original_language,
       w.year, w.runtime_ms, w.added_at, w.updated_at, w.added_by, w.description,
       w.lead_person_id, w.lead_person_sort_name, w.subtype
FROM work w
JOIN work_external_id e ON e.work_id = w.id
WHERE e.provider = ? AND e.external_id = ?;

-- An import names an album it already knows by the canonical title and artist, never the tags.
-- name: GetWorkNamingByExternalID :one
SELECT w.canonical_title, w.year, p.name AS lead_person_name
FROM work w
JOIN work_external_id e ON e.work_id = w.id
LEFT JOIN person p ON p.id = w.lead_person_id
WHERE e.provider = ? AND e.external_id = ?;

-- name: SetWorkLeadPerson :exec
-- The Tracks navigation level's own denormalized sort key (0021's
-- doc comment) -- written only by internal/db/library.People.AddCredit, the one place a
-- role='artist' ord=0 credit is ever recorded.
UPDATE work SET lead_person_id = ?, lead_person_sort_name = ? WHERE id = ?;

-- The list surface composes its own WHERE in internal/db/library/works.go: a filter
-- written here as an is-it-empty test is not sargable, so SQLite uses no index for that
-- column and sorts the whole table for every page (documentation/sql-and-sqlc.md).

-- name: UpdateWorkTimestamp :exec
UPDATE work SET updated_at = ? WHERE id = ?;

-- name: DeleteWork :exec
DELETE FROM work WHERE id = ?;

-- name: LinkWorkExternalID :exec
INSERT INTO work_external_id (provider, external_id, work_id, confidence, first_seen_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (provider, external_id) DO UPDATE SET
  work_id = excluded.work_id,
  confidence = excluded.confidence;

-- name: ListWorkExternalIDs :many
SELECT provider, external_id, work_id, confidence, first_seen_at
FROM work_external_id WHERE work_id = ? ORDER BY provider, external_id;

-- name: GetWorkExternalIDByProvider :one
SELECT provider, external_id, work_id, confidence, first_seen_at
FROM work_external_id WHERE work_id = ? AND provider = ?;

-- A work's year is never overwritten once set -- by a person, or by an earlier
-- enrichment pass -- so this is conditional rather than a plain UPDATE.
-- name: UpdateWorkYearIfNull :exec
UPDATE work SET year = ?, updated_at = ? WHERE id = ? AND year IS NULL;

-- Same "enrichment fills a gap, never overwrites" rule as year: a work
-- created before this column existed, or from a source with no MusicBrainz
-- answer, only gets its subtype backfilled here.
-- name: UpdateWorkSubtypeIfNull :exec
UPDATE work SET subtype = ?, updated_at = ? WHERE id = ? AND subtype IS NULL;

-- Unlike year, unconditional: nothing else writes a work's description yet,
-- so there is no earlier value worth protecting.
-- A work is named after its release group once enrichment has it, not after whichever file came first.
-- name: SetWorkTitle :exec
UPDATE work SET canonical_title = ?, sort_title = ?, updated_at = ? WHERE id = ?;

-- name: SetWorkDescription :exec
UPDATE work SET description = ?, updated_at = ? WHERE id = ?;

-- name: AddWorkTitle :one
INSERT INTO work_title (work_id, title, normalized, language, kind, source)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id, work_id, title, normalized, language, kind, source;

-- name: ListWorkTitles :many
SELECT id, work_id, title, normalized, language, kind, source
FROM work_title WHERE work_id = ? ORDER BY kind, title;

-- Every stored variant is matched during import, not just the canonical one (ADR-41).
-- name: FindWorksByNormalizedTitle :many
SELECT w.id, w.media_type, w.canonical_title, w.sort_title, w.original_language,
       w.year, w.runtime_ms, w.added_at, w.updated_at, w.added_by, w.description,
       w.lead_person_id, w.lead_person_sort_name, w.subtype
FROM work w
JOIN work_title t ON t.work_id = w.id
WHERE t.normalized = sqlc.arg(normalized)
  AND (sqlc.arg(media_type) = '' OR w.media_type = sqlc.arg(media_type))
GROUP BY w.id
ORDER BY w.sort_title, w.id
LIMIT sqlc.arg(page_size);

-- name: CreateEdition :one
INSERT INTO edition (work_id, kind, label, year, format, catalogue_number,
                     barcode, runtime_ms, is_default)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, work_id, kind, label, year, format, catalogue_number,
          barcode, runtime_ms, is_default;

-- name: GetEdition :one
SELECT id, work_id, kind, label, year, format, catalogue_number,
       barcode, runtime_ms, is_default
FROM edition WHERE id = ?;

-- name: ListEditions :many
SELECT id, work_id, kind, label, year, format, catalogue_number,
       barcode, runtime_ms, is_default
FROM edition WHERE work_id = ? ORDER BY is_default DESC, year, label;

-- name: GetEditionByExternalID :one
SELECT e.id, e.work_id, e.kind, e.label, e.year, e.format, e.catalogue_number,
       e.barcode, e.runtime_ms, e.is_default
FROM edition e
JOIN edition_external_id x ON x.edition_id = e.id
WHERE x.provider = ? AND x.external_id = ?;

-- name: LinkEditionExternalID :exec
INSERT INTO edition_external_id (provider, external_id, edition_id, confidence)
VALUES (?, ?, ?, ?)
ON CONFLICT (provider, external_id) DO UPDATE SET
  edition_id = excluded.edition_id,
  confidence = excluded.confidence;

-- name: ListEditionExternalIDs :many
SELECT provider, external_id, edition_id, confidence
FROM edition_external_id WHERE edition_id = ? ORDER BY provider, external_id;

-- Import revert's own use (todo 4.9): only ever called for an edition
-- CommitUnit itself created (import_unit.edition_created), and only after
-- every import_unit referencing it has been cleared (ClearImportUnitPlacement) --
-- edition_external_id cascades; import_unit.edition_id does not.
-- name: DeleteEdition :exec
DELETE FROM edition WHERE id = ?;

-- kind='unknown' is the cross-media-type placeholder marker an Identifier chain
-- leaves behind when it resolves an edition's external id but has no real
-- metadata yet (internal/db/library.EditionLink, internal/media/music's newEditionLink):
-- label is that external id itself, chosen so it can never collide with a real
-- edition's UNIQUE(work_id, kind, label). Once refined, kind changes away from
-- 'unknown' and the row stops being returned here -- no separate cursor needed,
-- the query's own result set shrinks as enrichment succeeds.
-- How many editions still wait on real metadata: an enrichment run's total, for the status bar.
-- name: CountPlaceholderEditions :one
SELECT CAST(COUNT(*) AS INTEGER) AS total FROM edition WHERE kind = 'unknown';

-- name: ListPlaceholderEditions :many
SELECT id, work_id, kind, label, year, format, catalogue_number,
       barcode, runtime_ms, is_default
FROM edition WHERE kind = 'unknown' AND id > sqlc.arg(after_id)
ORDER BY id LIMIT sqlc.arg(page_size);

-- A placeholder whose release no source knows any more, replaced by one chosen from the files:
-- no longer a placeholder, so enrichment stops asking about it.
-- name: WithdrawEdition :exec
UPDATE edition SET kind = 'withdrawn' WHERE id = ? AND kind = 'unknown';

-- name: UpdateEdition :exec
UPDATE edition SET kind = ?, year = ?, format = ?, catalogue_number = ?,
       barcode = ?, runtime_ms = ?
WHERE id = ?;

-- name: SetWorkField :exec
INSERT INTO work_field (work_id, field, provider, value, fetched_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (work_id, field, provider) DO UPDATE SET
  value = excluded.value,
  fetched_at = excluded.fetched_at;

-- name: ListWorkFields :many
SELECT work_id, field, provider, value, fetched_at
FROM work_field WHERE work_id = ? ORDER BY field, provider;

-- name: SetWorkDate :exec
INSERT INTO work_date (work_id, kind, region, date, source)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (work_id, kind, region) DO UPDATE SET
  date = excluded.date,
  source = excluded.source;

-- name: ListWorkDates :many
SELECT work_id, kind, region, date, source
FROM work_date WHERE work_id = ? ORDER BY kind, region;

-- The dashboard's fresh-works count (ADR-55, todo 4.20): every work with a
-- work_date of kind on or after the cutoff. Distinct since a work can carry
-- more than one region's own date row for the same kind.
-- name: CountFreshWorks :one
SELECT CAST(COUNT(*) AS INTEGER) AS total FROM work
WHERE EXISTS (SELECT 1 FROM work_date wd WHERE wd.work_id = work.id AND wd.kind = sqlc.arg(kind) AND wd.date >= sqlc.arg(date))
   OR EXISTS (SELECT 1 FROM rss_sighting rs JOIN desired_item di ON di.id = rs.desired_item_id
              WHERE di.work_id = work.id AND rs.seen_at >= sqlc.arg(rss_since));

-- name: SetWorkClassification :exec
INSERT INTO work_classification (work_id, kind, value, source)
VALUES (?, ?, ?, ?)
ON CONFLICT (work_id, kind, value) DO UPDATE SET source = excluded.source;

-- name: ListWorkClassifications :many
SELECT work_id, kind, value, source
FROM work_classification WHERE work_id = ? ORDER BY kind, value;

-- Monitoring on an album type is the point: "studio albums, not live".
-- name: FindWorksByClassification :many
SELECT w.id, w.media_type, w.canonical_title, w.sort_title, w.original_language,
       w.year, w.runtime_ms, w.added_at, w.updated_at, w.added_by, w.description,
       w.lead_person_id, w.lead_person_sort_name, w.subtype
FROM work w
JOIN work_classification c ON c.work_id = w.id
WHERE c.kind = sqlc.arg(kind) AND c.value = sqlc.arg(value)
  AND (sqlc.arg(media_type) = '' OR w.media_type = sqlc.arg(media_type))
ORDER BY w.sort_title, w.id
LIMIT sqlc.arg(page_size);

-- name: ClearWorkClassification :exec
DELETE FROM work_classification WHERE work_id = ? AND kind = ?;

-- name: CreateWorkPart :one
INSERT INTO work_part (work_id, media_ref, sort_key, title, runtime_ms, date, monitored)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id, work_id, media_ref, sort_key, title, runtime_ms, date, monitored, available_at, available_confidence, available_basis;

-- name: GetWorkPart :one
SELECT id, work_id, media_ref, sort_key, title, runtime_ms, date, monitored, available_at, available_confidence, available_basis
FROM work_part WHERE id = ?;

-- name: FindWorkPart :one
SELECT id, work_id, media_ref, sort_key, title, runtime_ms, date, monitored, available_at, available_confidence, available_basis
FROM work_part WHERE work_id = ? AND media_ref = ?;

-- Ordered by the module's sort key. Core never parses media_ref to order parts.
-- name: ListWorkParts :many
SELECT id, work_id, media_ref, sort_key, title, runtime_ms, date, monitored, available_at, available_confidence, available_basis
FROM work_part WHERE work_id = ? ORDER BY sort_key, id;

-- What a SET requires follows the monitored parts, so unmonitoring an episode
-- shrinks the season rather than leaving it permanently incomplete (Appendix E, D3).
-- name: CountMonitoredParts :one
SELECT CAST(COUNT(*) AS INTEGER) AS monitored_parts
FROM work_part WHERE work_id = ? AND monitored = 1;

-- name: SetWorkPartMonitored :exec
UPDATE work_part SET monitored = ? WHERE id = ?;

-- name: DeleteWorkPart :exec
DELETE FROM work_part WHERE id = ?;

-- name: LinkWorkPartExternalID :exec
INSERT INTO work_part_external_id (provider, external_id, part_id, confidence)
VALUES (?, ?, ?, ?)
ON CONFLICT (provider, external_id, part_id) DO UPDATE SET
  confidence = excluded.confidence;

-- One recording appears on many albums, so this returns every part sharing the
-- identifier. That is the cross-album duplicate question (ADR-35).
-- name: FindPartsByExternalID :many
SELECT p.id, p.work_id, p.media_ref, p.sort_key, p.title, p.runtime_ms, p.date, p.monitored, p.available_at, p.available_confidence, p.available_basis
FROM work_part p
JOIN work_part_external_id e ON e.part_id = p.id
WHERE e.provider = ? AND e.external_id = ?
ORDER BY p.work_id, p.sort_key, p.id;

-- name: ListWorkPartExternalIDs :many
SELECT provider, external_id, part_id, confidence
FROM work_part_external_id WHERE part_id = ? ORDER BY provider, external_id;

-- name: AddWorkPartTitle :one
INSERT INTO work_part_title (part_id, title, normalized, language, provider)
VALUES (?, ?, ?, ?, ?)
RETURNING id, part_id, title, normalized, language, provider;

-- name: ListWorkPartTitles :many
SELECT id, part_id, title, normalized, language, provider
FROM work_part_title WHERE part_id = ? ORDER BY provider, title;

-- name: AddAltNumbering :exec
INSERT INTO alt_numbering (part_id, scheme, provider, alt_ref)
VALUES (?, ?, ?, ?)
ON CONFLICT (part_id, scheme, provider) DO UPDATE SET
  alt_ref = excluded.alt_ref;

-- Every other number of a page of parts, one per scheme: the first provider's.
-- name: ListAltNumbersForParts :many
SELECT part_id, scheme, alt_ref FROM alt_numbering
WHERE part_id IN (sqlc.slice('part_ids'))
ORDER BY part_id, scheme, provider;

-- Anime absolute numbering: episode 137 of a series resolves to the part that
-- aired numbers do not name (ADR-41). Scoped to the work, or two series collide.
-- name: FindPartByAltNumber :one
SELECT p.id, p.work_id, p.media_ref, p.sort_key, p.title, p.runtime_ms, p.date, p.monitored, p.available_at, p.available_confidence, p.available_basis
FROM work_part p
JOIN alt_numbering a ON a.part_id = p.id
WHERE p.work_id = sqlc.arg(work_id)
  AND a.scheme = sqlc.arg(scheme)
  AND a.alt_ref = sqlc.arg(alt_ref);

-- Works identified at release-group level only (the text search never picks an
-- edition): no edition, and either not enriched yet (no credit) or with files on
-- disk still waiting for an edition to be chosen. 'musicbrainz:release-group'
-- must match internal/media/music.ProviderReleaseGroup.
-- name: ListEditionlessWorks :many
SELECT x.work_id AS work_id, CAST(MIN(x.external_id) AS TEXT) AS release_group_mbid
FROM work_external_id x
WHERE x.provider = 'musicbrainz:release-group'
  AND x.work_id > sqlc.arg(after_id)
  AND NOT EXISTS (SELECT 1 FROM edition e WHERE e.work_id = x.work_id)
  AND (
    NOT EXISTS (SELECT 1 FROM credit c WHERE c.work_id = x.work_id)
    OR EXISTS (SELECT 1 FROM work_file f WHERE f.work_id = x.work_id)
  )
GROUP BY x.work_id
ORDER BY x.work_id
LIMIT sqlc.arg(page_size);

-- How many files the library actually holds for one work. Removing an album
-- deletes the work row only when this is zero: with files on disk the work
-- would be recreated by the next scan, so deleting it would be a lie.
-- name: CountWorkFiles :one
SELECT CAST(COUNT(*) AS INTEGER) AS total FROM work_file WHERE work_id = ?;

-- Every file of one work, for deleting an album's files along with it.
-- name: ListWorkFiles :many
SELECT media_object_id FROM work_file WHERE work_id = ? ORDER BY media_object_id;

-- Releases in a date window that this library has a reason to care about:
-- either something wants the work, or an artist credited on it is monitored.
-- A work with several dates of the same kind in different regions would repeat,
-- so the earliest per work is taken -- a calendar shows one entry per release.
-- Only libraries the caller can see: a wanted work through its desired_item, a followed artist's
-- release through the subscription that follows them.
-- name: ListCalendarEntries :many
SELECT w.id, w.canonical_title, w.sort_title, w.subtype,
       COALESCE(pa.name, '') AS artist_name,
       MIN(wd.date) AS release_date,
       EXISTS (SELECT 1 FROM work_file wf JOIN library_path lp ON lp.media_object_id = wf.media_object_id
               WHERE wf.work_id = w.id AND lp.missing_since IS NULL) AS on_disk
FROM work_date wd
JOIN work w ON w.id = wd.work_id
LEFT JOIN person pa ON pa.id = w.lead_person_id
WHERE wd.kind = sqlc.arg(kind)
  AND wd.date >= sqlc.arg(from_date) AND wd.date <= sqlc.arg(to_date)
  AND (
    EXISTS (SELECT 1 FROM desired_item di WHERE di.work_id = w.id AND di.monitored = 1
            AND di.category_id IN (SELECT value FROM json_each(sqlc.arg(category_ids))))
    OR EXISTS (SELECT 1 FROM credit c
               JOIN person p ON p.id = c.person_id
               JOIN subscription s ON s.subject_type = 'person' AND s.subject_id = p.id AND s.enabled = 1
               WHERE c.work_id = w.id AND c.role = 'artist' AND p.monitored = 1
                 AND s.category_id IN (SELECT value FROM json_each(sqlc.arg(category_ids))))
  )
GROUP BY w.id
ORDER BY release_date, w.sort_title, w.id
LIMIT sqlc.arg(page_size);

-- How many releases fall on each date of a window, for the calendar's year view: the same works
-- ListCalendarEntries answers, counted rather than listed, so a busy year is never truncated.
-- name: CountCalendarDates :many
SELECT release_date, CAST(COUNT(*) AS INTEGER) AS releases
FROM (
  SELECT MIN(wd.date) AS release_date
  FROM work_date wd
  JOIN work w ON w.id = wd.work_id
  WHERE wd.kind = sqlc.arg(kind)
    AND wd.date >= sqlc.arg(from_date) AND wd.date <= sqlc.arg(to_date)
    AND (
      EXISTS (SELECT 1 FROM desired_item di WHERE di.work_id = w.id AND di.monitored = 1
              AND di.category_id IN (SELECT value FROM json_each(sqlc.arg(category_ids))))
      OR EXISTS (SELECT 1 FROM credit c
                 JOIN person p ON p.id = c.person_id
                 JOIN subscription s ON s.subject_type = 'person' AND s.subject_id = p.id AND s.enabled = 1
                 WHERE c.work_id = w.id AND c.role = 'artist' AND p.monitored = 1
                   AND s.category_id IN (SELECT value FROM json_each(sqlc.arg(category_ids))))
    )
  GROUP BY w.id
)
GROUP BY release_date
ORDER BY release_date;

-- A merged MusicBrainz id answers under its new id: the work follows it, unless another work
-- already holds the new one (M1f, 2026-09-25).
-- name: RepointWorkExternalID :execrows
UPDATE work_external_id SET external_id = sqlc.arg(new_id)
WHERE work_external_id.provider = sqlc.arg(provider) AND work_external_id.external_id = sqlc.arg(old_id)
  AND work_external_id.work_id = sqlc.arg(work_id)
  AND NOT EXISTS (SELECT 1 FROM work_external_id held
                  WHERE held.provider = sqlc.arg(provider) AND held.external_id = sqlc.arg(new_id));

-- name: RepointEditionExternalID :execrows
UPDATE edition_external_id SET external_id = sqlc.arg(new_id)
WHERE edition_external_id.provider = sqlc.arg(provider) AND edition_external_id.external_id = sqlc.arg(old_id)
  AND edition_external_id.edition_id = sqlc.arg(edition_id)
  AND NOT EXISTS (SELECT 1 FROM edition_external_id held
                  WHERE held.provider = sqlc.arg(provider) AND held.external_id = sqlc.arg(new_id));

-- A merged release's old id still names its edition, or files tagged with it open a second one.
-- name: KeepEditionAlias :exec
INSERT INTO edition_external_id (provider, external_id, edition_id, confidence)
VALUES (?, ?, ?, ?)
ON CONFLICT (provider, external_id) DO NOTHING;

-- Another edition of the same work under the same label that already has the kind a placeholder
-- is about to take: the same release, reached by a second id.
-- name: FindEditionTwin :one
SELECT twin.id FROM edition placeholder
JOIN edition twin ON twin.work_id = placeholder.work_id AND twin.label = placeholder.label
                 AND twin.id != placeholder.id
WHERE placeholder.id = sqlc.arg(edition_id) AND twin.kind = sqlc.arg(kind)
ORDER BY twin.id LIMIT 1;

-- name: MoveEditionExternalIDs :exec
UPDATE edition_external_id SET edition_id = sqlc.arg(to_id), confidence = sqlc.arg(confidence)
WHERE edition_id = sqlc.arg(from_id);

-- name: MoveEditionDesiredItems :exec
UPDATE desired_item SET edition_id = sqlc.arg(to_id) WHERE edition_id = sqlc.arg(from_id);

-- name: MoveEditionImportUnits :exec
UPDATE import_unit SET edition_id = sqlc.arg(to_id) WHERE edition_id = sqlc.arg(from_id);

-- name: MoveEditionPackItems :exec
UPDATE pack_item SET edition_id = sqlc.arg(to_id) WHERE edition_id = sqlc.arg(from_id);

-- A credit both editions hold stays the twin's own; the leftover goes with DeleteEditionCredits.
-- name: MoveEditionCredits :exec
UPDATE OR IGNORE credit SET edition_id = sqlc.arg(to_id) WHERE edition_id = sqlc.arg(from_id);

-- name: DeleteEditionCredits :exec
DELETE FROM credit WHERE edition_id = ?;

-- Every path of every file of one work, for returning the files to the identification queue.
-- name: ListWorkFilePaths :many
SELECT f.media_object_id, lp.root_folder_id, lp.rel_path
FROM work_file f
JOIN library_path lp ON lp.media_object_id = f.media_object_id
WHERE f.work_id = ?
ORDER BY f.media_object_id, lp.is_primary DESC, lp.id;

-- name: UnlinkWorkFiles :exec
DELETE FROM work_file WHERE work_id = ?;

-- name: DetachDesiredFilesForWork :exec
DELETE FROM desired_item_file WHERE desired_item_id IN (SELECT id FROM desired_item WHERE work_id = ?);

-- A person added this album (Discover, the add dialog), rather than a scan or an artist's expansion.
-- name: CountPersonAddedItems :one
SELECT CAST(COUNT(*) AS INTEGER) AS total FROM desired_item WHERE work_id = ? AND added_by IS NOT NULL;

-- name: ResetDesiredItemsForWork :exec
UPDATE desired_item SET present_count = 0, state = 'MISSING', dirty = 1 WHERE work_id = ?;

-- The unverified albums (only local ids, D6) a file is linked to, for when it is identified.
-- name: ListUnverifiedWorksForFile :many
SELECT wf.work_id FROM work_file wf
WHERE wf.media_object_id = ?
  AND EXISTS (SELECT 1 FROM work_external_id ux WHERE ux.work_id = wf.work_id)
  AND NOT EXISTS (SELECT 1 FROM work_external_id vx WHERE vx.work_id = wf.work_id AND vx.provider NOT LIKE 'local:%')
ORDER BY wf.work_id;

-- name: UnlinkWorkFile :exec
DELETE FROM work_file WHERE work_id = ? AND media_object_id = ?;

-- name: DetachDesiredFileForWork :exec
DELETE FROM desired_item_file
WHERE media_object_id = sqlc.arg(media_object_id)
  AND desired_item_id IN (SELECT id FROM desired_item WHERE work_id = sqlc.arg(work_id));

-- Whether any unverified album exists, so a scan skips the per-file check on a library with none.
-- name: AnyUnverifiedWork :one
SELECT CAST(EXISTS (SELECT 1 FROM work_external_id WHERE provider = 'local:album') AS INTEGER) AS present;

-- TV episodes in a date window, for the calendar: monitored episodes of series the caller's libraries hold.
-- name: ListCalendarEpisodes :many
SELECT p.id AS part_id, p.work_id, w.canonical_title, p.media_ref, p.title, p.date,
       (SELECT MIN(d.category_id) FROM desired_item d WHERE d.work_id = w.id
          AND d.category_id IN (SELECT value FROM json_each(sqlc.arg(category_ids)))) AS category_id,
       EXISTS (SELECT 1 FROM desired_item_file f JOIN desired_item d2 ON d2.id = f.desired_item_id
               JOIN library_path lp ON lp.media_object_id = f.media_object_id
               WHERE d2.work_id = p.work_id AND f.media_ref = p.media_ref AND f.role = 'primary'
                 AND lp.missing_since IS NULL) AS on_disk
FROM work_part p
JOIN work w ON w.id = p.work_id
WHERE w.media_type = 'tv' AND p.monitored = 1
  AND p.date >= sqlc.arg(from_date) AND p.date <= sqlc.arg(to_date)
  AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = w.id
              AND d.category_id IN (SELECT value FROM json_each(sqlc.arg(category_ids))))
ORDER BY p.date, w.sort_title, p.sort_key
LIMIT sqlc.arg(page_size);

-- name: CountCalendarEpisodeDates :many
SELECT p.date AS release_date, CAST(COUNT(*) AS INTEGER) AS releases
FROM work_part p
JOIN work w ON w.id = p.work_id
WHERE w.media_type = 'tv' AND p.monitored = 1
  AND p.date >= sqlc.arg(from_date) AND p.date <= sqlc.arg(to_date)
  AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = w.id
              AND d.category_id IN (SELECT value FROM json_each(sqlc.arg(category_ids))))
GROUP BY p.date
ORDER BY p.date;

-- One page of a work's parts, in one group (its parts "<group>:..."), or all of them with group ''.
-- name: ListWorkPartsPage :many
SELECT id, work_id, media_ref, sort_key, title, runtime_ms, date, monitored, available_at, available_confidence, available_basis
FROM work_part
WHERE work_id = sqlc.arg(work_id)
  AND (CAST(sqlc.arg(group_ref) AS TEXT) = '' OR substr(media_ref, 1, length(CAST(sqlc.arg(group_ref) AS TEXT)) + 1) = CAST(sqlc.arg(group_ref) AS TEXT) || ':')
  AND (sort_key > sqlc.arg(after_sort_key) OR (sort_key = sqlc.arg(after_sort_key) AND id > sqlc.arg(after_id)))
ORDER BY sort_key, id LIMIT sqlc.arg(page_size);

-- A library's missing parts, newest first: monitored, out, and no file covers them (Sonarr's Wanted, Missing).
-- name: ListWantedParts :many
SELECT p.id AS part_id, p.work_id, w.canonical_title AS work_title, p.media_ref, p.title, p.date, p.available_at
FROM work_part p
JOIN work w ON w.id = p.work_id
WHERE EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = w.id AND d.category_id = sqlc.arg(category_id))
  AND p.monitored = 1
  AND ((p.available_at IS NOT NULL AND p.available_at <= sqlc.arg(now))
       OR (p.available_at IS NULL AND p.date IS NOT NULL AND p.date <= sqlc.arg(today)))
  AND NOT EXISTS (SELECT 1 FROM desired_item_file f JOIN desired_item d2 ON d2.id = f.desired_item_id
                  WHERE d2.work_id = p.work_id AND f.media_ref = p.media_ref AND f.role = 'primary')
  AND (COALESCE(p.date, '') < sqlc.arg(after_date) OR (COALESCE(p.date, '') = sqlc.arg(after_date) AND p.id < sqlc.arg(after_id)))
ORDER BY COALESCE(p.date, '') DESC, p.id DESC
LIMIT sqlc.arg(page_size);

-- A work has files while one of them has a path no sweep found gone.
-- name: WorkHasLiveFile :one
SELECT CAST(EXISTS (SELECT 1 FROM work_file wf JOIN library_path lp ON lp.media_object_id = wf.media_object_id
                    WHERE wf.work_id = ? AND lp.missing_since IS NULL) AS INTEGER) AS present;
