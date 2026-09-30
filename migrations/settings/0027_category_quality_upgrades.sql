-- Whether a library upgrades files below its cutoff at all (asked 2026-09-30; Lidarr's "Upgrades Allowed").
ALTER TABLE category ADD COLUMN quality_upgrades INTEGER NOT NULL DEFAULT 1 CHECK (quality_upgrades IN (0, 1));
