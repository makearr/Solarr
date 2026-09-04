-- Section 11.7's three separate concepts (decided 2026-09-04, never
-- conflated): writable is a probed fact, read_only is the user's policy,
-- accepts_new (category_root_folder, migration 0001) is per-category
-- draining. The implemented table had only id/path/label/online.
ALTER TABLE root_folder ADD COLUMN writable INTEGER NOT NULL DEFAULT 1 CHECK (writable IN (0, 1));
-- Set alongside writable when a probe finds it false, so the reason survives
-- past the single log line that first reported it (section 20.2's own
-- "reported, never corrected" principle, applied to placement's own read).
ALTER TABLE root_folder ADD COLUMN writable_reason TEXT;
-- The user's own policy: "this is my curated library, look but do not
-- touch". Blocks new placement, upgrade-in-place and tag write-back, and
-- blocks renaming unless allow_rename is also set (Invariant 69).
ALTER TABLE root_folder ADD COLUMN read_only INTEGER NOT NULL DEFAULT 0 CHECK (read_only IN (0, 1));
ALTER TABLE root_folder ADD COLUMN allow_rename INTEGER NOT NULL DEFAULT 0 CHECK (allow_rename IN (0, 1));
-- Capacity is polled, not assumed (section 11.7): a maintenance job fills
-- these in, and Placement.Resolve trusts whatever it last read rather than
-- probing the filesystem itself.
ALTER TABLE root_folder ADD COLUMN total_bytes INTEGER;
ALTER TABLE root_folder ADD COLUMN free_bytes INTEGER;
-- The user's own reserve: placement refuses a root once free_bytes minus an
-- item's size would fall below this.
ALTER TABLE root_folder ADD COLUMN min_free_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE root_folder ADD COLUMN capacity_checked_at INTEGER;
-- Identifies the filesystem a root lives on, for the ADR-11 tiebreak: two
-- roots sharing an fs_id can reflink or hardlink between them instead of
-- copying. Deliberately opaque to this schema -- whatever the capacity job
-- decides identifies a filesystem stably (a device id today, something
-- ZFS-dataset-aware later) is free to change without a migration.
ALTER TABLE root_folder ADD COLUMN fs_id TEXT;
-- Probed filesystem capabilities (reflink support, case sensitivity, ...) as
-- JSON -- deliberately not columns, since which capabilities matter is
-- expected to grow before any of them earns a WHERE clause of its own.
ALTER TABLE root_folder ADD COLUMN capabilities_json TEXT;

-- Section 11.7's COMMIT step: what an affinity key ("artist:1234",
-- "series:88") last resolved to, so keeping an artist's discography or a
-- show's seasons together survives past the one item that first triggered
-- it. One row per (category, key) -- a key means something different in a
-- different category's namespace even if the string collides.
CREATE TABLE placement_affinity (
  category_id    INTEGER NOT NULL REFERENCES category(id),
  affinity_key   TEXT    NOT NULL,
  root_folder_id INTEGER NOT NULL REFERENCES root_folder(id),
  updated_at     INTEGER NOT NULL,
  PRIMARY KEY (category_id, affinity_key)
) WITHOUT ROWID;

CREATE INDEX placement_affinity_by_root ON placement_affinity(root_folder_id);
