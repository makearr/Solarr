-- A temporary bridge (2026-09-03): the works list needs to filter by category
-- (ADR-27), but a category is settings.db policy attached to root folders, and
-- there is no path from work to root folder yet -- desired_item is the eventual
-- real link, but desired_item.profile_id is NOT NULL and no profile system exists
-- before stage 4. This table records which root folder(s) a work's identifying
-- scan came from, so category filtering works today without inventing stage-4
-- policy early. Superseded once desired_item is populated for real music items.

CREATE TABLE work_root_folder (
  work_id        INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  -- No foreign key: root_folder lives in settings.db (ADR-25, section 4.3).
  root_folder_id INTEGER NOT NULL,
  first_seen_at  INTEGER NOT NULL,
  PRIMARY KEY (work_id, root_folder_id)
) WITHOUT ROWID;
CREATE INDEX work_root_folder_by_root ON work_root_folder(root_folder_id);
