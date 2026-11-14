-- A deleted row's id is never handed out again: jobs, caches and open pages outside library.db still hold it.
CREATE TABLE id_high_water (name TEXT PRIMARY KEY, value INTEGER NOT NULL) WITHOUT ROWID;

INSERT INTO id_high_water (name, value) SELECT 'work', COALESCE(MAX(id), 0) FROM work;
INSERT INTO id_high_water (name, value) SELECT 'media_object', COALESCE(MAX(id), 0) FROM media_object;
INSERT INTO id_high_water (name, value) SELECT 'desired_item', COALESCE(MAX(id), 0) FROM desired_item;
INSERT INTO id_high_water (name, value) SELECT 'acquisition', COALESCE(MAX(id), 0) FROM acquisition;

CREATE TRIGGER work_high_water AFTER DELETE ON work BEGIN
  UPDATE id_high_water SET value = MAX(value, OLD.id) WHERE name = 'work';
END;

CREATE TRIGGER media_object_high_water AFTER DELETE ON media_object BEGIN
  UPDATE id_high_water SET value = MAX(value, OLD.id) WHERE name = 'media_object';
END;

CREATE TRIGGER desired_item_high_water AFTER DELETE ON desired_item BEGIN
  UPDATE id_high_water SET value = MAX(value, OLD.id) WHERE name = 'desired_item';
END;

CREATE TRIGGER acquisition_high_water AFTER DELETE ON acquisition BEGIN
  UPDATE id_high_water SET value = MAX(value, OLD.id) WHERE name = 'acquisition';
END;
