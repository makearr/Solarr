-- Deleting a finished job checks that no job depends on it; without this each one scanned the whole job table.
CREATE INDEX job_by_dependency ON job(depends_on) WHERE depends_on IS NOT NULL;
