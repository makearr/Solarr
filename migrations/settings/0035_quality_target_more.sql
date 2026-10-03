-- More of what a library can aim for: audio channels, languages (comma separated, any of), audio and video bitrate and
-- frame rate. The file type column may now hold several, comma separated.
ALTER TABLE category_quality_target ADD COLUMN audio_channels     TEXT NOT NULL DEFAULT '';
ALTER TABLE category_quality_target ADD COLUMN audio_languages    TEXT NOT NULL DEFAULT '';
ALTER TABLE category_quality_target ADD COLUMN subtitle_languages TEXT NOT NULL DEFAULT '';
ALTER TABLE category_quality_target ADD COLUMN audio_bitrate_kbps INTEGER;
ALTER TABLE category_quality_target ADD COLUMN video_bitrate_kbps INTEGER;
ALTER TABLE category_quality_target ADD COLUMN frame_rate         REAL;
