-- FTS5 over titles and aliases (architecture.md's indexing-at-scale section; ADR-28's
-- 150ms search target, section 21.5). Pure ASCII, or sqlc emits wrong SQL that still
-- compiles.
--
-- One row per title variant -- canonical, alias, part title -- so a match on any of them
-- surfaces its work. Own table, triggers keep it synchronised: FTS5 has no foreign keys,
-- and content= external-content tables need exactly one content row per indexed row, which
-- a work with several titles does not give it.
--
-- rowid is assigned deterministically from the source row (id*4 + a per-table tag) so the
-- triggers below can target a row directly by rowid. An UNINDEXED column has no index at
-- all, so filtering by anything other than rowid or MATCH is a full scan of the table.
CREATE VIRTUAL TABLE title_search USING fts5(
  title,
  work_id UNINDEXED,
  media_type UNINDEXED,
  tokenize = 'unicode61 remove_diacritics 2'
);

-- work: rowid = id*4 + 0. Every work has exactly one canonical_title, always present.
CREATE TRIGGER title_search_work_ai AFTER INSERT ON work BEGIN
  INSERT INTO title_search(rowid, title, work_id, media_type)
  VALUES (NEW.id * 4, NEW.canonical_title, NEW.id, NEW.media_type);
END;
CREATE TRIGGER title_search_work_au AFTER UPDATE OF canonical_title, media_type ON work BEGIN
  UPDATE title_search SET title = NEW.canonical_title, media_type = NEW.media_type
  WHERE rowid = NEW.id * 4;
END;
CREATE TRIGGER title_search_work_ad AFTER DELETE ON work BEGIN
  DELETE FROM title_search WHERE rowid = OLD.id * 4;
END;

-- work_title: rowid = id*4 + 1. Aliases and alternate-language titles (ADR-41).
CREATE TRIGGER title_search_work_title_ai AFTER INSERT ON work_title BEGIN
  INSERT INTO title_search(rowid, title, work_id, media_type)
  SELECT NEW.id * 4 + 1, NEW.title, NEW.work_id, w.media_type
  FROM work w WHERE w.id = NEW.work_id;
END;
CREATE TRIGGER title_search_work_title_au AFTER UPDATE OF title ON work_title BEGIN
  UPDATE title_search SET title = NEW.title WHERE rowid = NEW.id * 4 + 1;
END;
CREATE TRIGGER title_search_work_title_ad AFTER DELETE ON work_title BEGIN
  DELETE FROM title_search WHERE rowid = OLD.id * 4 + 1;
END;

-- work_part: rowid = id*4 + 2. title is nullable -- a part is not always named.
CREATE TRIGGER title_search_work_part_ai AFTER INSERT ON work_part
WHEN NEW.title IS NOT NULL BEGIN
  INSERT INTO title_search(rowid, title, work_id, media_type)
  SELECT NEW.id * 4 + 2, NEW.title, NEW.work_id, w.media_type
  FROM work w WHERE w.id = NEW.work_id;
END;
-- Delete-then-insert, guarded on the new value, covers title becoming or ceasing to be
-- NULL and not only changing between two values.
CREATE TRIGGER title_search_work_part_au AFTER UPDATE OF title ON work_part BEGIN
  DELETE FROM title_search WHERE rowid = NEW.id * 4 + 2;
  INSERT INTO title_search(rowid, title, work_id, media_type)
  SELECT NEW.id * 4 + 2, NEW.title, NEW.work_id, w.media_type
  FROM work w WHERE w.id = NEW.work_id AND NEW.title IS NOT NULL;
END;
CREATE TRIGGER title_search_work_part_ad AFTER DELETE ON work_part BEGIN
  DELETE FROM title_search WHERE rowid = OLD.id * 4 + 2;
END;

-- work_part_title: rowid = id*4 + 3. Alt titles per part (ADR-41).
CREATE TRIGGER title_search_work_part_title_ai AFTER INSERT ON work_part_title BEGIN
  INSERT INTO title_search(rowid, title, work_id, media_type)
  SELECT NEW.id * 4 + 3, NEW.title, p.work_id, w.media_type
  FROM work_part p JOIN work w ON w.id = p.work_id WHERE p.id = NEW.part_id;
END;
CREATE TRIGGER title_search_work_part_title_au AFTER UPDATE OF title ON work_part_title BEGIN
  UPDATE title_search SET title = NEW.title WHERE rowid = NEW.id * 4 + 3;
END;
CREATE TRIGGER title_search_work_part_title_ad AFTER DELETE ON work_part_title BEGIN
  DELETE FROM title_search WHERE rowid = OLD.id * 4 + 3;
END;
