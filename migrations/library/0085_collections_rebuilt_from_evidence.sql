-- A film found under a series' TMDB id joined that series' franchise; a franchise holds works of its own type only.
DELETE FROM collection_member
WHERE collection_id IN (SELECT id FROM collection WHERE kind = 'franchise')
  AND work_id IN (SELECT w.id FROM work w JOIN collection c ON c.id = collection_member.collection_id WHERE w.media_type <> c.media_type);

-- Wikidata's franchises are read again: Solarr's own groups were once merged into them, and one tied by a spin-off or a
-- sequel is called after its oldest series now.
DELETE FROM work_franchise_check
WHERE work_id IN (SELECT m.work_id FROM collection_member m JOIN collection c ON c.id = m.collection_id WHERE c.source = 'wikidata');
DELETE FROM collection_member WHERE collection_id IN (SELECT id FROM collection WHERE source = 'wikidata');
