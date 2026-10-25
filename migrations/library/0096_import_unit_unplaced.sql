-- The import retry finds the units a library could not take (decided COMMIT, never placed) without reading every unit.
CREATE INDEX idx_import_unit_unplaced ON import_unit(session_id) WHERE decision = 'COMMIT' AND work_id IS NULL;
