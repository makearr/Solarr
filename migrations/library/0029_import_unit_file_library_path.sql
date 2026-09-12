-- import_unit_file's own back-reference to which library_path row a unit's
-- placement actually created -- closing a real bug the 4.22 linking audit
-- found. ImportRevert.quarantineObject had no way to tell "the path this unit
-- placed" apart from every other path sharing the same bytes, so whenever
-- RegisterBatch's own quick-hash dedupe merged this unit's placement into a
-- media_object an unrelated, untouched commit already owned, reverting this
-- session quarantined and deleted that unrelated library entry too
-- (Invariant 92: "removing from one category never deletes bytes another
-- path still references").
--
-- Nullable, and backfilled only where it is unambiguous: a media_object that
-- already had more than one library_path row before this migration ran has
-- no way to know retroactively which path belonged to which unit, so those
-- rows stay NULL and ImportRevert refuses to guess for them rather than
-- risk repeating the bug it exists to close.
ALTER TABLE import_unit_file ADD COLUMN library_path_id INTEGER REFERENCES library_path(id) ON DELETE SET NULL;

UPDATE import_unit_file
SET library_path_id = (
  SELECT lp.id FROM library_path lp WHERE lp.media_object_id = import_unit_file.media_object_id
)
WHERE (
  SELECT count(*) FROM library_path lp WHERE lp.media_object_id = import_unit_file.media_object_id
) = 1;
