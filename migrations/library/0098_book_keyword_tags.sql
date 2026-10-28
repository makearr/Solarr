-- Book genres took every Open Library, Wikidata and Audible subject as a tag; a book now keeps only the genres the
-- tree holds, so the tags those subjects made go, with their assignments. Shipped and a person's own tags stay.
DELETE FROM tag WHERE genre_tree = 'books' AND source NOT IN ('shipped', 'user')
  AND NOT EXISTS (SELECT 1 FROM tag_closure c JOIN tag d ON d.id = c.descendant_id
                  WHERE c.ancestor_id = tag.id AND d.source IN ('shipped', 'user'));
