-- A tag belongs to a taxonomy a media type declares ('music', 'screen'), or to none ('', a free label); a
-- taxonomy's tags carry its key in their slug so two taxonomies may both have a Comedy (plan 10-libraries L1).
ALTER TABLE tag ADD COLUMN taxonomy TEXT NOT NULL DEFAULT '';

-- Every provider tag so far is a music genre.
UPDATE tag SET taxonomy = 'music', slug = 'music/' || slug WHERE source IN ('musicbrainz', 'wikidata');

-- Which version of a taxonomy's shipped tree was last applied.
CREATE TABLE tag_taxonomy (
  key            TEXT    PRIMARY KEY,
  seeded_version INTEGER NOT NULL
) WITHOUT ROWID;

CREATE INDEX tag_alias_alias ON tag_alias(alias);
