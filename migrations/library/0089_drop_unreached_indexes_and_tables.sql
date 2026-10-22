-- Indexes no query reaches and a table nothing uses: each one only cost writes and space.

-- Covered by a UNIQUE key that starts with the same column.
DROP INDEX edition_by_work;
DROP INDEX work_release_filter_work;

-- Nothing filters or sorts by these columns.
DROP INDEX work_part_title_norm;
DROP INDEX media_object_afp;
DROP INDEX blocked_by_chromaprint;
DROP INDEX media_attributes_res;
DROP INDEX rss_sighting_seen;

-- The hunt's range on eligible_after stops either index ordering by priority; hunt_by_release serves the prefix.
DROP INDEX hunt_by_searched;

-- Nothing has read or written this since series collections are rebuilt every pass.
DROP TABLE work_franchise_check;
