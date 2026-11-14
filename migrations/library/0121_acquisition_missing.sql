-- When the download client first failed to list a download; a download is declared removed only after it stays gone.
CREATE TABLE acquisition_missing (
  acquisition_id INTEGER PRIMARY KEY REFERENCES acquisition(id) ON DELETE CASCADE,
  since          INTEGER NOT NULL
);
