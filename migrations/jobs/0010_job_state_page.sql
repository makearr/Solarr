-- A page of one state pages on the index instead of walking the other states' rows.
CREATE INDEX job_by_state_page ON job(state, updated_at DESC, id DESC);
