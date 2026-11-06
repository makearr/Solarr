-- A quality target a person gave an item of its own, over its library's; the target lives in settings.db, so no foreign key.
CREATE TABLE desired_target_override (
  desired_item_id INTEGER PRIMARY KEY REFERENCES desired_item(id) ON DELETE CASCADE,
  target_id       INTEGER NOT NULL
) WITHOUT ROWID;
