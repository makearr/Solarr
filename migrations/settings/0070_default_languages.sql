-- The languages a new library prefers, asked in the setup wizard. Existing settings carry over: the audio languages of the
-- first non-anime library's profile become the defaults, and a library without a language profile takes that profile.
CREATE TABLE default_language (
  code TEXT NOT NULL PRIMARY KEY,
  ord  INTEGER NOT NULL
) WITHOUT ROWID;

INSERT INTO default_language (code, ord)
SELECT value, key FROM json_each((SELECT audio_preferred_json FROM language_profile
  WHERE id = (SELECT language_profile_id FROM category WHERE language_profile_id IS NOT NULL AND kind <> 'anime' ORDER BY id LIMIT 1)))
WHERE value <> 'ja';

UPDATE category SET language_profile_id = (SELECT c2.language_profile_id FROM category c2 WHERE c2.language_profile_id IS NOT NULL AND c2.kind <> 'anime' ORDER BY c2.id LIMIT 1)
WHERE language_profile_id IS NULL AND kind <> 'anime';
