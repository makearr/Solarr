-- A tag can be hearted too; SQLite cannot widen a CHECK, so the table is rebuilt.
CREATE TABLE favorite_new (
  user_id      INTEGER NOT NULL,
  subject_type TEXT    NOT NULL CHECK (subject_type IN ('work', 'work_part', 'person', 'organisation', 'collection', 'tag')),
  subject_id   INTEGER NOT NULL,
  created_at   INTEGER NOT NULL,
  PRIMARY KEY (user_id, subject_type, subject_id)
) WITHOUT ROWID;
INSERT INTO favorite_new (user_id, subject_type, subject_id, created_at) SELECT user_id, subject_type, subject_id, created_at FROM favorite;
DROP TABLE favorite;
ALTER TABLE favorite_new RENAME TO favorite;
