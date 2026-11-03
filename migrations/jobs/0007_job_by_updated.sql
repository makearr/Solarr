-- The /jobs list orders every state by (updated_at, id); without this it sorted the whole table.
CREATE INDEX job_by_updated ON job(updated_at DESC, id DESC);
