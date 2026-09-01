-- Jobs carry the category they act on, so a job listing can be filtered by the
-- caller's per-category access (ADR-27). A job progress bar naming an album
-- from a hidden category is a leak, and the listing is one of the surfaces the
-- ADR names explicitly.
--
-- Added now rather than when the first category-scoped job type exists,
-- because ADR-27 is blunt about the alternative: retrofitting an authorisation
-- filter into queries that already exist is how these features never get built.
--
-- NULL means the job is not about any one category -- a backup, a database
-- maintenance pass. Those are system work, and who may see them is a question
-- about roles rather than about categories.
ALTER TABLE job ADD COLUMN category_id INTEGER;

CREATE INDEX job_by_category ON job(category_id, updated_at) WHERE category_id IS NOT NULL;
