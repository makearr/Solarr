-- Wikidata is now read for a whole library at once and names a collection only by a franchise item: the collections
-- emptied by 0085 are made again with that name; one somebody follows is kept.
DELETE FROM collection
WHERE media_type = 'tv' AND source = 'wikidata' AND monitored = 0
  AND NOT EXISTS (SELECT 1 FROM collection_member m WHERE m.collection_id = collection.id);
