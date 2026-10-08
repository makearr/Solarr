-- Which libraries a person or organisation appears in, by role: the one People and Organisations list for every
-- media type, and the visibility rule (plan/09-shared-core.md section 4). Role '' is any role, counted once per work.
CREATE TABLE person_library (
  person_id   INTEGER NOT NULL REFERENCES person(id) ON DELETE CASCADE,
  category_id INTEGER NOT NULL,
  role        TEXT    NOT NULL,
  works       INTEGER NOT NULL,
  latest      INTEGER,
  PRIMARY KEY (person_id, category_id, role)
) WITHOUT ROWID;
CREATE INDEX person_library_by_category ON person_library(category_id, role, person_id);

CREATE TABLE organisation_library (
  organisation_id INTEGER NOT NULL REFERENCES organisation(id) ON DELETE CASCADE,
  category_id     INTEGER NOT NULL,
  role            TEXT    NOT NULL,
  works           INTEGER NOT NULL,
  latest          INTEGER,
  PRIMARY KEY (organisation_id, category_id, role)
) WITHOUT ROWID;
CREATE INDEX organisation_library_by_category ON organisation_library(category_id, role, organisation_id);

INSERT INTO person_library (person_id, category_id, role, works, latest)
SELECT c.person_id, d.category_id, c.role, COUNT(DISTINCT c.work_id), MAX(w.year)
FROM credit c JOIN work w ON w.id = c.work_id JOIN desired_item d ON d.work_id = c.work_id
GROUP BY c.person_id, d.category_id, c.role;
INSERT INTO person_library (person_id, category_id, role, works, latest)
SELECT c.person_id, d.category_id, '', COUNT(DISTINCT c.work_id), MAX(w.year)
FROM credit c JOIN work w ON w.id = c.work_id JOIN desired_item d ON d.work_id = c.work_id
GROUP BY c.person_id, d.category_id;

INSERT INTO organisation_library (organisation_id, category_id, role, works, latest)
SELECT o.organisation_id, d.category_id, o.role, COUNT(DISTINCT o.work_id), MAX(w.year)
FROM work_organisation o JOIN work w ON w.id = o.work_id JOIN desired_item d ON d.work_id = o.work_id
GROUP BY o.organisation_id, d.category_id, o.role;
INSERT INTO organisation_library (organisation_id, category_id, role, works, latest)
SELECT o.organisation_id, d.category_id, '', COUNT(DISTINCT o.work_id), MAX(w.year)
FROM work_organisation o JOIN work w ON w.id = o.work_id JOIN desired_item d ON d.work_id = o.work_id
GROUP BY o.organisation_id, d.category_id;

-- A work the user removed and asked not to see again, for any media type: a scan, a follow or a list leaves it out.
CREATE TABLE work_exclusion (
  media_type  TEXT    NOT NULL,
  provider    TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  title       TEXT    NOT NULL,
  created_at  INTEGER NOT NULL,
  PRIMARY KEY (provider, external_id)
) WITHOUT ROWID;
INSERT INTO work_exclusion (media_type, provider, external_id, title, created_at)
SELECT 'tv', provider, external_id, title, created_at FROM series_exclusion;
DROP TABLE series_exclusion;
