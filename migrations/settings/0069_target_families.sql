-- Target qualities belong to a family of media types (video, music, spoken, book), not to one type: the shipped
-- presets were stored once for movies, series and adult, which hold the same files. Duplicates are merged and the
-- libraries that picked one follow the survivor.
UPDATE category_target SET target_id = (
  SELECT MIN(k.id) FROM quality_target k JOIN quality_target t ON t.name = k.name AND k.builtin = 1 AND k.media_type IN ('movie', 'tv', 'adult')
  WHERE t.id = category_target.target_id)
WHERE target_id IN (SELECT id FROM quality_target WHERE builtin = 1 AND media_type IN ('movie', 'tv', 'adult'));

DELETE FROM quality_target WHERE builtin = 1 AND media_type IN ('movie', 'tv', 'adult')
  AND id NOT IN (SELECT MIN(id) FROM quality_target WHERE builtin = 1 AND media_type IN ('movie', 'tv', 'adult') GROUP BY name);

-- A target of the user's own that would now share a name with another in the family keeps both, told apart by type.
UPDATE quality_target SET name = name || ' (' || media_type || ')'
WHERE media_type IN ('movie', 'tv', 'adult', 'youtube', 'news') AND EXISTS (
  SELECT 1 FROM quality_target o WHERE o.id <> quality_target.id AND o.name = quality_target.name
    AND o.media_type IN ('movie', 'tv', 'adult', 'youtube', 'news') AND (o.builtin = 1 OR o.id < quality_target.id));

UPDATE quality_target SET media_type = 'video' WHERE media_type IN ('movie', 'tv', 'adult', 'youtube', 'news');
UPDATE quality_target SET media_type = 'spoken' WHERE media_type IN ('audiobook', 'podcast');
UPDATE quality_target SET media_type = 'book' WHERE media_type = 'comic';

UPDATE target_profile SET media_types = 'video' WHERE media_types = 'movie,tv,adult';
UPDATE target_profile SET media_types = 'spoken' WHERE media_types = 'audiobook';
