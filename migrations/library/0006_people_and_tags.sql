-- People, organisations, tags and subscriptions (ADR-32, ADR-33) (ADR-25).

-- First-class monitorable entities, not just credits (ADR-32).
CREATE TABLE person (
  id             INTEGER PRIMARY KEY,
  name           TEXT    NOT NULL,
  sort_name      TEXT    NOT NULL,
  disambiguation TEXT,
  kind           TEXT,
  gender         TEXT,
  country        TEXT,
  -- Partial dates allowed: 1979, 1979-04, 1979-04-12.
  born           TEXT,
  died           TEXT,
  fields_json    TEXT,
  monitored      INTEGER NOT NULL DEFAULT 0 CHECK (monitored IN (0, 1)),
  added_at       INTEGER NOT NULL
);
CREATE INDEX person_sort ON person(sort_name);
CREATE INDEX person_monitored ON person(id) WHERE monitored = 1;

-- Load-bearing, not decoration: a stage name is often the only way to connect a
-- release to a person, and it feeds the Identifier chain (ADR-24).
CREATE TABLE person_alias (
  person_id  INTEGER NOT NULL REFERENCES person(id) ON DELETE CASCADE,
  alias      TEXT    NOT NULL,
  normalized TEXT    NOT NULL,
  source     TEXT    NOT NULL,
  PRIMARY KEY (person_id, alias)
) WITHOUT ROWID;
CREATE INDEX person_alias_norm ON person_alias(normalized);

CREATE TABLE person_external_id (
  provider    TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  person_id   INTEGER NOT NULL REFERENCES person(id) ON DELETE CASCADE,
  PRIMARY KEY (provider, external_id)
) WITHOUT ROWID;
CREATE INDEX person_external_by_person ON person_external_id(person_id);

-- Roles are the point: subscribing to Htuacmasn as director and not as producer needs the role
-- stored per credit, not per person (ADR-32, F13).
-- edition_id and part_id scope a credit below the work: a narrator belongs to the
-- audiobook edition, a performer to the track (Appendix E, D2). Zero means unscoped,
-- and they are NOT NULL because SQLite admits NULL into a PRIMARY KEY and would then
-- stop deduplicating; work_id stays so the cascade still reaches every credit.
CREATE TABLE credit (
  person_id  INTEGER NOT NULL REFERENCES person(id) ON DELETE CASCADE,
  work_id    INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  edition_id INTEGER NOT NULL DEFAULT 0,
  part_id    INTEGER NOT NULL DEFAULT 0,
  role       TEXT    NOT NULL,
  character  TEXT    NOT NULL DEFAULT '',
  ord        INTEGER,
  PRIMARY KEY (person_id, work_id, edition_id, part_id, role, character)
);
CREATE INDEX credit_by_work ON credit(work_id, role);
CREATE INDEX credit_by_person ON credit(person_id, role);
CREATE INDEX credit_by_part ON credit(part_id, role) WHERE part_id != 0;
CREATE INDEX credit_by_edition ON credit(edition_id, role) WHERE edition_id != 0;

-- Studios, networks, labels, publishers, imprints and sites are one entity.
CREATE TABLE organisation (
  id          INTEGER PRIMARY KEY,
  name        TEXT    NOT NULL,
  sort_name   TEXT    NOT NULL,
  kind        TEXT    NOT NULL,
  -- Sub-labels, imprints and sub-sites are real in every domain.
  parent_id   INTEGER REFERENCES organisation(id),
  country     TEXT,
  fields_json TEXT,
  monitored   INTEGER NOT NULL DEFAULT 0 CHECK (monitored IN (0, 1))
);
CREATE INDEX organisation_parent ON organisation(parent_id);
CREATE INDEX organisation_sort ON organisation(sort_name);

CREATE TABLE organisation_external_id (
  provider        TEXT    NOT NULL,
  external_id     TEXT    NOT NULL,
  organisation_id INTEGER NOT NULL REFERENCES organisation(id) ON DELETE CASCADE,
  PRIMARY KEY (provider, external_id)
) WITHOUT ROWID;
CREATE INDEX organisation_external_by_org ON organisation_external_id(organisation_id);

CREATE TABLE work_organisation (
  work_id         INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  organisation_id INTEGER NOT NULL REFERENCES organisation(id) ON DELETE CASCADE,
  role            TEXT    NOT NULL,
  PRIMARY KEY (work_id, organisation_id, role)
) WITHOUT ROWID;
CREATE INDEX work_organisation_by_org ON work_organisation(organisation_id, role);

-- Hierarchical (ADR-33). Source separates curation from provider data, so a
-- refresh never removes what a user assigned.
CREATE TABLE tag (
  id          INTEGER PRIMARY KEY,
  name        TEXT    NOT NULL,
  slug        TEXT    NOT NULL UNIQUE,
  parent_id   INTEGER REFERENCES tag(id),
  description TEXT,
  source      TEXT    NOT NULL,
  UNIQUE(parent_id, name)
);

CREATE TABLE tag_alias (
  tag_id INTEGER NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
  alias  TEXT    NOT NULL,
  PRIMARY KEY (tag_id, alias)
) WITHOUT ROWID;

-- Materialised so "this tag or any descendant" is a join rather than a recursive
-- query per call. Tags number in the hundreds; the rows they filter do not.
CREATE TABLE tag_closure (
  ancestor_id   INTEGER NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
  descendant_id INTEGER NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
  depth         INTEGER NOT NULL,
  PRIMARY KEY (ancestor_id, descendant_id)
) WITHOUT ROWID;
CREATE INDEX tag_closure_desc ON tag_closure(descendant_id);

CREATE TABLE tag_assignment (
  tag_id       INTEGER NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
  subject_type TEXT    NOT NULL,
  subject_id   INTEGER NOT NULL,
  source       TEXT    NOT NULL,
  PRIMARY KEY (tag_id, subject_type, subject_id, source)
) WITHOUT ROWID;
CREATE INDEX tag_assignment_subject ON tag_assignment(subject_type, subject_id);

-- Media types x roles x filters (ADR-32).
CREATE TABLE subscription (
  id                     INTEGER PRIMARY KEY,
  subject_type           TEXT    NOT NULL
                         CHECK (subject_type IN ('person', 'organisation', 'tag')),
  subject_id             INTEGER NOT NULL,
  media_types_json       TEXT    NOT NULL,
  -- Null is any role.
  roles_json             TEXT,
  category_id            INTEGER,
  profile_id             INTEGER,
  filters_json           TEXT,
  -- Monitoring forward is cheap; the back catalogue is an explicit choice.
  monitor_back_catalogue INTEGER NOT NULL DEFAULT 0
                         CHECK (monitor_back_catalogue IN (0, 1)),
  enabled                INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  user_id                INTEGER,
  created_at             INTEGER NOT NULL,
  UNIQUE(subject_type, subject_id, user_id)
);
CREATE INDEX subscription_subject ON subscription(subject_type, subject_id) WHERE enabled = 1;

-- Crossovers, collaborations, spin-offs and side projects (F12).
CREATE TABLE relationship (
  id        INTEGER PRIMARY KEY,
  from_type TEXT    NOT NULL,
  from_id   INTEGER NOT NULL,
  to_type   TEXT    NOT NULL,
  to_id     INTEGER NOT NULL,
  kind      TEXT    NOT NULL,
  UNIQUE(from_type, from_id, to_type, to_id, kind)
);
CREATE INDEX relationship_to ON relationship(to_type, to_id);
