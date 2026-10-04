-- A watched download folder must name its library from now on. One that named none imported into the first
-- music library; that choice is written down here so those folders keep importing where they did.
UPDATE download_folder
SET category_id = (SELECT id FROM category WHERE media_type = 'music' ORDER BY priority_rank, id LIMIT 1)
WHERE watch = 1 AND category_id IS NULL;
