-- ADR-51's drain state machine: DRAINING forces accepts_new = 0 for every
-- category on this root (a move job evacuates it to the remaining roots),
-- DRAINED means it holds no library files and can be unmounted -- the root
-- itself is not deleted from the configuration, the user does that.
ALTER TABLE root_folder ADD COLUMN drain_state TEXT NOT NULL DEFAULT 'NONE'
  CHECK (drain_state IN ('NONE', 'DRAINING', 'PAUSED', 'DRAINED'));
-- library.db's move_job.id -- no FK, root_folder lives in settings.db
-- (ADR-25, section 4.3, the same cross-db rule library_path.root_folder_id
-- already follows).
ALTER TABLE root_folder ADD COLUMN drain_job_id INTEGER;
