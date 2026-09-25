-- Profiles are grouped by the media type they judge (2026-09-25): a library is offered only its own.
ALTER TABLE quality_profile ADD COLUMN media_type TEXT NOT NULL DEFAULT 'music';
