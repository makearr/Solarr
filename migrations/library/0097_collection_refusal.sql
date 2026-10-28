-- A provider's collection a person deleted (work_id 0) or took a work out of: no refresh or pass makes it again.
CREATE TABLE collection_refusal (
  provider    TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  work_id     INTEGER NOT NULL,
  PRIMARY KEY (provider, external_id, work_id)
) WITHOUT ROWID;
