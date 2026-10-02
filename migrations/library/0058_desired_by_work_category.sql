-- The list's visibility test asks "does this work have a desired item in the library" per work; with only a
-- (category_id, state) index SQLite scanned every item of the category for every work, and a filtered list
-- (Wanted, Cutoff unmet, Fresh) of a 93,000-album library took over a minute. Found 2026-10-02.
CREATE INDEX desired_by_work_category ON desired_item(work_id, category_id);
