-- A library may aim for the target worked out from its first quality profile instead of a stored one.
CREATE TABLE category_target_auto (
  category_id INTEGER PRIMARY KEY REFERENCES category(id) ON DELETE CASCADE
);
