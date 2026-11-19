-- What the last full metadata write of a work held, so a refresh that finds the provider unchanged writes nothing.
-- Any change to a work's parts or credits made by another writer drops the digest, so the next refresh writes in full.
CREATE TABLE work_write_digest (
  work_id    INTEGER PRIMARY KEY REFERENCES work(id) ON DELETE CASCADE,
  digest     BLOB    NOT NULL,
  written_at INTEGER NOT NULL
) WITHOUT ROWID;

CREATE TRIGGER work_part_insert_drops_digest AFTER INSERT ON work_part
BEGIN
  DELETE FROM work_write_digest WHERE work_id = NEW.work_id;
END;

CREATE TRIGGER work_part_update_drops_digest AFTER UPDATE ON work_part
BEGIN
  DELETE FROM work_write_digest WHERE work_id IN (OLD.work_id, NEW.work_id);
END;

CREATE TRIGGER work_part_delete_drops_digest AFTER DELETE ON work_part
BEGIN
  DELETE FROM work_write_digest WHERE work_id = OLD.work_id;
END;

CREATE TRIGGER credit_insert_drops_digest AFTER INSERT ON credit
BEGIN
  DELETE FROM work_write_digest WHERE work_id = NEW.work_id;
END;

CREATE TRIGGER credit_update_drops_digest AFTER UPDATE ON credit
BEGIN
  DELETE FROM work_write_digest WHERE work_id IN (OLD.work_id, NEW.work_id);
END;

CREATE TRIGGER credit_delete_drops_digest AFTER DELETE ON credit
BEGIN
  DELETE FROM work_write_digest WHERE work_id = OLD.work_id;
END;
