-- Jobs carry the category they act on, so a job listing can be filtered by the caller's per-
-- category access (ADR-27).
ALTER TABLE job ADD COLUMN category_id INTEGER;

CREATE INDEX job_by_category ON job(category_id, updated_at) WHERE category_id IS NOT NULL;
