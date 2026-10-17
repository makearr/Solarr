-- The metadata providers a library uses, best first; a provider left out is not asked. A library with no rows follows the
-- instance's order for its media type.
CREATE TABLE category_metadata_source (
  category_id INTEGER NOT NULL REFERENCES category(id) ON DELETE CASCADE,
  source      TEXT    NOT NULL,
  ord         INTEGER NOT NULL,
  PRIMARY KEY (category_id, source)
) WITHOUT ROWID;
