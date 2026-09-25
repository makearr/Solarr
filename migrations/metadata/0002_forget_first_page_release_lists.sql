-- Release lists cached before paging held only the first 25 releases of a group
-- (MusicBrainz sends 25 a page with tracklists); forget them so they are read in full.
DELETE FROM metadata_cache WHERE kind = 'musicbrainz:releases';
