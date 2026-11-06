-- Content profiles (what kind of material a target is for, with recommended settings), the target's link to one, and presets for sources the first set missed.
CREATE TABLE target_profile (
  id                 INTEGER PRIMARY KEY,
  name               TEXT NOT NULL UNIQUE,
  media_types        TEXT NOT NULL,
  description        TEXT NOT NULL DEFAULT '',
  resolution         TEXT NOT NULL DEFAULT '',
  video_codec        TEXT NOT NULL DEFAULT '',
  frame_rate         REAL,
  max_frame_rate     REAL,
  crf                REAL,
  encoder_preset     TEXT NOT NULL DEFAULT '',
  encoder_tune       TEXT NOT NULL DEFAULT '',
  deinterlace        INTEGER NOT NULL DEFAULT 0 CHECK (deinterlace IN (0, 1)),
  audio_codec        TEXT NOT NULL DEFAULT '',
  audio_channels     TEXT NOT NULL DEFAULT '',
  audio_bitrate_kbps INTEGER,
  sample_rate_hz     INTEGER,
  bit_depth          INTEGER,
  bitrate_kbps       INTEGER,
  recommended_target TEXT NOT NULL DEFAULT '',
  builtin            INTEGER NOT NULL DEFAULT 0 CHECK (builtin IN (0, 1))
);

ALTER TABLE quality_target ADD COLUMN profile_id INTEGER REFERENCES target_profile(id) ON DELETE SET NULL;
ALTER TABLE quality_target ADD COLUMN encoder_tune TEXT NOT NULL DEFAULT '';
ALTER TABLE quality_target ADD COLUMN deinterlace INTEGER NOT NULL DEFAULT 0 CHECK (deinterlace IN (0, 1));

INSERT INTO target_profile (name, media_types, description, resolution, video_codec, frame_rate, max_frame_rate, crf, encoder_preset, encoder_tune, deinterlace,
  audio_codec, audio_channels, audio_bitrate_kbps, sample_rate_hz, bit_depth, bitrate_kbps, recommended_target, builtin) VALUES
('4K', 'movie,tv,adult', 'Ultra-high definition with high dynamic range: keep the frame rate, 10-bit, a generous quality.', '2160p', 'h265', NULL, NULL, 22, 'slower', '', 0, 'aac', '2.0', 192, NULL, 10, NULL, '2160p, source fps, H.265 10-bit, MKV, AC3 5.1 640 kbit/s, 12,000 kbit/s or CRF 22, slower', 1),
('Sports', 'movie,tv,adult', 'Fast motion and often interlaced broadcasts: up to 60 fps, deinterlaced, a little more bitrate to keep motion clean.', '1080p', 'h265', NULL, 60, 23, 'slow', '', 1, 'aac', '2.0', 192, NULL, NULL, NULL, '1080p, source fps (max 60), H.265, MP4, AAC stereo 256 kbit/s, 4,000 kbit/s or CRF 22, slow', 1),
('Anime', 'movie,tv,adult', 'Flat colours and sharp lines: animation tune, 10-bit to avoid banding, 1080p.', '1080p', 'h265', NULL, NULL, 24, 'slow', 'animation', 0, 'aac', '2.0', 160, NULL, NULL, NULL, '1080p, source fps, H.265, MKV, AAC stereo 160 kbit/s, 2,500 kbit/s or CRF 25, slow', 1),
('Old movies', 'movie,tv,adult', 'Film grain and soft detail: grain tune so the grain is kept, a lower CRF, source frame rate.', '1080p', 'h265', NULL, NULL, 22, 'slower', 'grain', 0, 'aac', '2.0', 160, NULL, 10, NULL, '1080p, source fps, H.265, MKV, AAC stereo 192 kbit/s, 3,000 kbit/s or CRF 24, slow', 1),
('TV series', 'movie,tv,adult', 'Live-action episodes: 1080p H.265 at a moderate CRF, small files for long series.', '1080p', 'h265', NULL, NULL, 26, 'slow', '', 0, 'aac', '2.0', 128, NULL, NULL, NULL, '1080p, 30 fps, H.265, MP4, AAC stereo 128 kbit/s, 1,600 kbit/s or CRF 28, slower', 1),
('Cartoon', 'movie,tv,adult', 'Western animation with flat colours: animation tune, 720p is plenty, a higher CRF.', '720p', 'h265', NULL, NULL, 27, 'medium', 'animation', 0, 'aac', '2.0', 96, NULL, NULL, NULL, '720p, 30 fps, H.265, MP4, AAC stereo 96 kbit/s, 1,000 kbit/s or CRF 28, medium', 1),
('CRT / VHS', 'movie,tv,adult', 'Tape, DVD and old broadcast sources: standard definition, deinterlaced, no point encoding bigger than the source.', '480p', 'h265', NULL, NULL, 26, 'slow', '', 1, 'aac', '2.0', 96, NULL, NULL, NULL, '480p, source fps, H.265, MP4, AAC stereo 96 kbit/s, 800 kbit/s or CRF 26, slow', 1),
('Radio', 'music', 'Broadcast and streaming sources: lossy stereo at a modest bitrate is all the source holds.', '', '', NULL, NULL, NULL, '', '', 0, 'aac', '2.0', NULL, 48000, NULL, 128, 'AAC, stereo, 48 kHz, 128 kbit/s, M4A', 1),
('CD', 'music', 'Ripped CDs: exactly CD quality, 44.1 kHz and 16-bit, lossless.', '', '', NULL, NULL, NULL, '', '', 0, 'flac', '2.0', NULL, 44100, 16, NULL, 'FLAC, stereo, 44.1 kHz, 16-bit, FLAC', 1),
('Lossless', 'music', 'Studio and download releases: lossless at 48 kHz and 16-bit.', '', '', NULL, NULL, NULL, '', '', 0, 'flac', '2.0', NULL, 48000, 16, NULL, 'FLAC, stereo, 48 kHz, 16-bit, FLAC', 1),
('Spoken', 'audiobook', 'Audiobooks with music or effects: stereo AAC with chapters.', '', '', NULL, NULL, NULL, '', '', 0, 'aac', '2.0', NULL, NULL, NULL, 96, 'AAC, stereo, 96 kbit/s, M4B, chapters', 1),
('Spoken (mono)', 'audiobook', 'Narration only: mono AAC is half the size and loses nothing.', '', '', NULL, NULL, NULL, '', '', 0, 'aac', '1.0', NULL, NULL, NULL, 64, 'AAC, mono, 64 kbit/s, M4B, chapters', 1);

INSERT OR IGNORE INTO quality_target (name, media_type, audio_codec, container, bitrate_kbps, tolerance_pct, resolution, video_codec, hdr, audio_channels, audio_languages, subtitle_languages, audio_bitrate_kbps, video_bitrate_kbps, frame_rate, crf, prefer_over, ignore_below, label, description, sample_rate_hz, bit_depth, encoder_preset, max_frame_rate, chapters, builtin) VALUES
('480p, source fps, H.265, MP4, AAC stereo 96 kbit/s, 800 kbit/s or CRF 26, slow', 'movie', 'aac', 'mp4', NULL, 0, '480p', 'h265', '', '2.0', '', '', 96, 800, NULL, 26, 0, 0, 'SD (CRT / VHS)', 'Standard definition for tape and DVD sources: 480p at the source frame rate.', NULL, NULL, 'slow', NULL, 0, 1),
('576p, source fps, H.265, MP4, AAC stereo 96 kbit/s, 1,000 kbit/s or CRF 25, slow', 'movie', 'aac', 'mp4', NULL, 0, '576p', 'h265', '', '2.0', '', '', 96, 1000, NULL, 25, 0, 0, 'SD PAL', 'Standard definition for PAL tape and DVD sources: 576p at the source frame rate.', NULL, NULL, 'slow', NULL, 0, 1),
('480p, source fps, H.265, MP4, AAC stereo 96 kbit/s, 800 kbit/s or CRF 26, slow', 'tv', 'aac', 'mp4', NULL, 0, '480p', 'h265', '', '2.0', '', '', 96, 800, NULL, 26, 0, 0, 'SD (CRT / VHS)', 'Standard definition for tape and DVD sources: 480p at the source frame rate.', NULL, NULL, 'slow', NULL, 0, 1),
('576p, source fps, H.265, MP4, AAC stereo 96 kbit/s, 1,000 kbit/s or CRF 25, slow', 'tv', 'aac', 'mp4', NULL, 0, '576p', 'h265', '', '2.0', '', '', 96, 1000, NULL, 25, 0, 0, 'SD PAL', 'Standard definition for PAL tape and DVD sources: 576p at the source frame rate.', NULL, NULL, 'slow', NULL, 0, 1),
('480p, source fps, H.265, MP4, AAC stereo 96 kbit/s, 800 kbit/s or CRF 26, slow', 'adult', 'aac', 'mp4', NULL, 0, '480p', 'h265', '', '2.0', '', '', 96, 800, NULL, 26, 0, 0, 'SD (CRT / VHS)', 'Standard definition for tape and DVD sources: 480p at the source frame rate.', NULL, NULL, 'slow', NULL, 0, 1),
('576p, source fps, H.265, MP4, AAC stereo 96 kbit/s, 1,000 kbit/s or CRF 25, slow', 'adult', 'aac', 'mp4', NULL, 0, '576p', 'h265', '', '2.0', '', '', 96, 1000, NULL, 25, 0, 0, 'SD PAL', 'Standard definition for PAL tape and DVD sources: 576p at the source frame rate.', NULL, NULL, 'slow', NULL, 0, 1),
('FLAC, stereo, 44.1 kHz, 16-bit, FLAC', 'music', 'flac', 'flac', NULL, 10, '', '', '', '2.0', '', '', NULL, NULL, NULL, NULL, 0, 0, 'CD quality', '', 44100, 16, '', NULL, 0, 1);
