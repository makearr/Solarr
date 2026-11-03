-- Whether a library's files already on disk may be renamed to match its naming: never, on request, or automatically.
ALTER TABLE naming_profile ADD COLUMN rename_existing TEXT NOT NULL DEFAULT 'on_request'
  CHECK (rename_existing IN ('never', 'on_request', 'automatic'));
