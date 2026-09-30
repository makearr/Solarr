-- Artists sort by the name as shown, as Lidarr does: a provider's "Doci, Vutu" filed Vutu Doci under D.
UPDATE person SET sort_name = name WHERE sort_name != name;
UPDATE work SET primary_artist_sort_name = (SELECT p.name FROM person p WHERE p.id = work.primary_artist_id)
WHERE primary_artist_id IS NOT NULL;
