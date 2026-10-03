-- A video library's target is a picture quality, not an audio codec: resolution, video codec and HDR join the
-- audio fields, each empty when the library does not care.
ALTER TABLE category_quality_target ADD COLUMN resolution  TEXT NOT NULL DEFAULT '';
ALTER TABLE category_quality_target ADD COLUMN video_codec TEXT NOT NULL DEFAULT '';
ALTER TABLE category_quality_target ADD COLUMN hdr         TEXT NOT NULL DEFAULT '';
