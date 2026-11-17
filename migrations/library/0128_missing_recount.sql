-- A marking of paths missing whose recount of the works is not finished; a stop between the two leaves it for the next start.
CREATE TABLE missing_recount (
  root_folder_id INTEGER NOT NULL,
  missing_since  INTEGER NOT NULL,
  PRIMARY KEY (root_folder_id, missing_since)
) WITHOUT ROWID;
