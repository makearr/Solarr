-- TMDB numbers movies and series apart, and companies and networks: movies and studios get keys of their own. Before
-- this a movie folder could resolve to the series of the same number and write the movie over it.
UPDATE work_external_id SET provider = 'tmdb:movie'
WHERE provider = 'tmdb' AND work_id IN (SELECT id FROM work WHERE media_type = 'movie');

UPDATE organisation_external_id SET provider = 'tmdb:company'
WHERE provider = 'tmdb' AND organisation_id IN (SELECT id FROM organisation WHERE kind = 'studio');

-- A studio that matched a network by number gave the network its logo; the network's own comes back on refresh.
UPDATE organisation SET fields_json = json_remove(fields_json, '$.logo')
WHERE kind = 'network' AND fields_json IS NOT NULL AND id IN (
  SELECT wo.organisation_id FROM work_organisation wo JOIN work w ON w.id = wo.work_id
  WHERE w.media_type = 'movie' AND wo.role = 'studio');

DELETE FROM work_organisation
WHERE role = 'studio' AND work_id IN (SELECT id FROM work WHERE media_type = 'movie')
  AND organisation_id IN (SELECT id FROM organisation WHERE kind = 'network');

-- A series a movie was written over carries a movie certification; what the movie left is taken off it again.
CREATE TABLE repair_0084_series (work_id INTEGER PRIMARY KEY);
INSERT INTO repair_0084_series (work_id)
SELECT DISTINCT c.work_id FROM work_certification c JOIN work w ON w.id = c.work_id
WHERE w.media_type = 'tv' AND c.system = 'movie';

-- The movie's IMDb id arrived with the movie's fields.
DELETE FROM work_external_id
WHERE provider = 'imdb' AND work_id IN (SELECT work_id FROM repair_0084_series)
  AND first_seen_at >= (SELECT MIN(f.fetched_at) FROM work_field f
                        WHERE f.work_id = work_external_id.work_id AND f.provider = 'tmdb'
                          AND f.field IN ('in_cinemas', 'digital_release', 'physical_release', 'tagline', 'trailer', 'studio'));

-- The movie's file is let go, to be found as the movie it is by the next scan.
DELETE FROM work_file
WHERE work_id IN (SELECT work_id FROM repair_0084_series)
  AND media_object_id IN (SELECT f.media_object_id FROM desired_item_file f JOIN desired_item d ON d.id = f.desired_item_id
                          WHERE d.work_id = work_file.work_id AND d.media_ref = '');
DELETE FROM desired_item WHERE media_ref = '' AND work_id IN (SELECT work_id FROM repair_0084_series);

DELETE FROM work_certification WHERE system = 'movie' AND work_id IN (SELECT work_id FROM repair_0084_series);
DELETE FROM work_organisation WHERE role = 'studio' AND work_id IN (SELECT work_id FROM repair_0084_series);
DELETE FROM work_field WHERE provider = 'tmdb' AND work_id IN (SELECT work_id FROM repair_0084_series);
DELETE FROM work_classification WHERE source = 'tmdb' AND work_id IN (SELECT work_id FROM repair_0084_series);
DELETE FROM work_title WHERE source = 'movie-merge' AND work_id IN (SELECT work_id FROM repair_0084_series);
DELETE FROM work_date WHERE kind = 'release' AND work_id IN (SELECT work_id FROM repair_0084_series);
DELETE FROM work_rating WHERE work_id IN (SELECT work_id FROM repair_0084_series);
DELETE FROM credit WHERE part_id = 0 AND work_id IN (SELECT work_id FROM repair_0084_series);
DELETE FROM collection_member
WHERE work_id IN (SELECT work_id FROM repair_0084_series)
  AND collection_id IN (SELECT id FROM collection WHERE media_type = 'movie');

-- The series is read again first, which puts back its own title, overview, pictures, genres and cast.
UPDATE work SET updated_at = 0 WHERE id IN (SELECT work_id FROM repair_0084_series);
DELETE FROM work_franchise_check WHERE work_id IN (SELECT work_id FROM repair_0084_series);

DROP TABLE repair_0084_series;
