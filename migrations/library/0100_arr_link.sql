-- What an *Arr item is here: the work, person or organisation an import matched or made for it, so a connection that sends
-- monitoring back touches only what it brought in. source_kind is the *Arr's own word for the resource.
CREATE TABLE arr_link (
  instance_id INTEGER NOT NULL,
  source_kind TEXT    NOT NULL,
  source_id   TEXT    NOT NULL,
  local_kind  TEXT    NOT NULL CHECK (local_kind IN ('work', 'person', 'organisation')),
  local_id    INTEGER NOT NULL,
  PRIMARY KEY (instance_id, source_kind, source_id)
) WITHOUT ROWID;
CREATE INDEX arr_link_by_instance ON arr_link(instance_id);
