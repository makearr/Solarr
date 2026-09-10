-- Todo 4.21's speed half: a one-off, tag-free pre-count pass
-- (inspect.EstimateUnits) gives the Import screen a total to show progress and
-- an ETA against. NULL means "not estimated yet" (a session predating this
-- column, or the estimate pass has not run), distinct from a real zero-unit
-- tree -- units_total below already means "found so far", so this is a
-- second, separate column rather than overloading that one's meaning.
ALTER TABLE import_session ADD COLUMN units_estimated INTEGER;
