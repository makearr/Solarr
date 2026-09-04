-- A work's own free-text description: a provider-sourced summary (Wikidata's
-- description plus, when the work has an English Wikipedia article, its
-- summary extract -- MusicBrainz itself carries no such field on a release
-- group). No source/kind column: unlike tag_assignment, nothing else writes
-- here yet, so there is nothing to distinguish from.
ALTER TABLE work ADD COLUMN description TEXT;
