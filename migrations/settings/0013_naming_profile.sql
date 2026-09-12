-- Section 11.8's own "templates as data" -- todo 4.5's remaining checklist item, scoped to the
-- schema alone on the user's own word: there is exactly one real template configuration in the
-- whole system today (music's shipped default, which reproduces the existing library byte for
-- byte), so this makes a second one pluggable later as a data-only change -- one insert plus one
-- category.naming_profile_id update -- rather than a further schema migration, without inventing
-- a second preset, a CRUD surface, or a template-editor screen nothing needs yet. No code reads
-- this table: internal/media/music.Committer/RenameStart still compile the same fixed
-- DefaultFolderTemplate/DefaultFileTemplate pair they always have (rename.go's own doc comment
-- already named this exact follow-up).
CREATE TABLE naming_profile (
  id              INTEGER PRIMARY KEY,
  name            TEXT    NOT NULL UNIQUE,
  folder_template TEXT    NOT NULL,
  file_template   TEXT    NOT NULL
);

-- Nullable, like quality_profile_id/language_profile_id (0004): absent means "use the shipped
-- default", not "use nothing".
ALTER TABLE category ADD COLUMN naming_profile_id INTEGER REFERENCES naming_profile(id);

-- The shipped music default (internal/media/music.DefaultFolderTemplate/DefaultFileTemplate),
-- reproduced here as data rather than referenced from Go, matching quality_profile's own shipped
-- rows (0012): this table's own row exists independently of the Go constant that still governs
-- actual behaviour until todo 4.5's remaining wiring step gives it a reader.
INSERT INTO naming_profile (name, folder_template, file_template) VALUES
  ('Default (music)', '{Artist Name}/{Album Title} ({Release Year})', '{Artist Name} - {track:00} - {Track Title}');
