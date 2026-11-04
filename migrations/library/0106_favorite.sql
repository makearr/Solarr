-- A person's hearts: items they want to find again, apart from a follow (which acquires). No foreign key: app_user lives in settings.db.
CREATE TABLE favorite (
  user_id      INTEGER NOT NULL,
  subject_type TEXT    NOT NULL CHECK (subject_type IN ('work', 'work_part', 'person', 'organisation', 'collection')),
  subject_id   INTEGER NOT NULL,
  created_at   INTEGER NOT NULL,
  PRIMARY KEY (user_id, subject_type, subject_id)
) WITHOUT ROWID;
