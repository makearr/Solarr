-- Stage 8 T2: generic extension points TV needs and other media types reuse (ADR-23). No TV-named column.

-- When a part becomes acquirable: an episode's air time, a pre-release track, a podcast episode, an on-sale date.
-- NULL means unknown; confidence and basis say how far to trust it (section 19.2).
ALTER TABLE work_part ADD COLUMN available_at INTEGER;
ALTER TABLE work_part ADD COLUMN available_confidence TEXT NOT NULL DEFAULT 'unknown'
  CHECK (available_confidence IN ('unknown', 'low', 'medium', 'high'));
ALTER TABLE work_part ADD COLUMN available_basis TEXT NOT NULL DEFAULT '';
CREATE INDEX work_part_available ON work_part(available_at) WHERE available_at IS NOT NULL;

-- A collection may name one part of a work rather than the whole work: a crossover event across series, a story arc
-- across volumes. part_id 0 is the whole work (the only meaning before this migration), so the key needs it.
CREATE TABLE collection_member_new (
  collection_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
  work_id       INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  part_id       INTEGER NOT NULL DEFAULT 0,
  ord           INTEGER,
  PRIMARY KEY (collection_id, work_id, part_id)
) WITHOUT ROWID;
INSERT INTO collection_member_new (collection_id, work_id, part_id, ord)
SELECT collection_id, work_id, 0, ord FROM collection_member;
DROP TABLE collection_member;
ALTER TABLE collection_member_new RENAME TO collection_member;
CREATE INDEX collection_member_work ON collection_member(work_id);

-- Per-work release filters: Sonarr release profiles, Medusa and SickGear per-show require/ignore words and
-- release-group allow and deny lists. media_ref_prefix narrows a rule to part of the work ('s03'); '' is all of it.
CREATE TABLE work_release_filter (
  id               INTEGER PRIMARY KEY,
  work_id          INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  media_ref_prefix TEXT    NOT NULL DEFAULT '',
  kind             TEXT    NOT NULL CHECK (kind IN ('must_contain', 'must_not_contain', 'prefer', 'group_allow', 'group_deny')),
  value            TEXT    NOT NULL,
  is_regex         INTEGER NOT NULL DEFAULT 0 CHECK (is_regex IN (0, 1)),
  -- Additive weight for 'prefer'; ignored by the other kinds.
  score            INTEGER NOT NULL DEFAULT 0,
  created_at       INTEGER NOT NULL,
  UNIQUE (work_id, media_ref_prefix, kind, value)
);
CREATE INDEX work_release_filter_work ON work_release_filter(work_id);
