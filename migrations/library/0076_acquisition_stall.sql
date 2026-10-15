-- Stalled-download cleanup: how far each download had got when last checked, and how many checks found it still.
CREATE TABLE acquisition_stall (
  acquisition_id INTEGER PRIMARY KEY REFERENCES acquisition(id) ON DELETE CASCADE,
  strikes        INTEGER NOT NULL DEFAULT 0,
  left_bytes     INTEGER NOT NULL,
  progress_at    INTEGER NOT NULL,
  checked_at     INTEGER NOT NULL
);
