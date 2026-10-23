-- Solarr's own offline declaration for an unreachable root, cleared when it is back (user, 2026-10-23);
-- a person's declaration keeps offline_auto 0 and is never cleared by a probe (Invariant 87).
ALTER TABLE root_folder ADD COLUMN offline_auto INTEGER NOT NULL DEFAULT 0 CHECK (offline_auto IN (0, 1));
ALTER TABLE root_folder ADD COLUMN offline_reason TEXT;
-- 1 when the root held any entry when last reached: an empty root that held some is an unmounted one.
ALTER TABLE root_folder ADD COLUMN seen_entries INTEGER NOT NULL DEFAULT 0;
