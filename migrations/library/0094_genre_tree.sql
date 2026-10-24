-- "Taxonomy" was renamed "genre tree" (user, 2026-10-24): the set a tag belongs to, music's or film and TV's; a tag
-- in none is a free label. Shipped tags were marked "taxonomy" and are now "shipped".
ALTER TABLE tag RENAME COLUMN taxonomy TO genre_tree;
ALTER TABLE tag_taxonomy RENAME TO genre_tree;
UPDATE tag SET source = 'shipped' WHERE source = 'taxonomy';
