-- Shipped default quality profiles (ADR-40, todo 4.3's own remaining checklist item):
-- four ready-made presets so a fresh install has something usable before anyone opens
-- the profile builder. Fully editable and removable like any other row this table
-- holds -- quality_profile carries no builtin flag the way exclusion_pattern does,
-- since a profile in use by a category is already protected by category.quality_profile_id's
-- own FK, and one not in use is safe to delete freely.
--
-- "AAC 160/48kHz/16-bit (m4a)" reproduces the worked example in 03-recognition.md's own
-- ADR-40 section byte for byte (the user's own real preference: prefer aac, 160 kbit,
-- 48 kHz, m4a, 16-bit stereo, in that order) -- proof this schema needs no code change
-- per profile, only data.
--
-- upgrade_json is the same min_delta/min_file_age/max_per_window/window worked example
-- from the same section for all four: no acquisition planner exists yet to make one
-- profile's upgrade cadence differ from another's in practice (stage 5/6).
INSERT INTO quality_profile (name, rules_json, upgrade_json) VALUES
  (
    'Lossless preferred',
    '{"quality_profile":"Lossless preferred","compare":["lossless","sample_rate","bit_depth","bitrate_distance","audio_codec","score"],"tiers":{"audio_codec":[["flac","alac"],["ogg","opus"],["aac"],["mp3"]]},"targets":{"bitrate":[{"when":{"audio_codec":"mp3"},"min":"192kbps","ideal":"320kbps","max":"400kbps"},{"when":{"audio_codec":"aac"},"min":"160kbps","ideal":"256kbps","max":"320kbps"}]}}',
    '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}'
  ),
  (
    'Lossy 320',
    '{"quality_profile":"Lossy 320","compare":["audio_codec","bitrate_distance","score"],"tiers":{"audio_codec":[["mp3","aac","ogg","opus"],["flac","alac"]]},"targets":{"bitrate":[{"when":{"audio_codec":"mp3"},"min":"256kbps","ideal":"320kbps","max":"320kbps"},{"when":{"audio_codec":"aac"},"min":"224kbps","ideal":"256kbps","max":"320kbps"},{"when":{"audio_codec":"opus"},"min":"160kbps","ideal":"192kbps","max":"256kbps"}]}}',
    '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}'
  ),
  (
    'Space conscious',
    '{"quality_profile":"Space conscious","compare":["audio_codec","bitrate_distance","score"],"tiers":{"audio_codec":[["opus"],["aac"],["ogg"],["mp3"],["flac","alac"]]},"targets":{"bitrate":[{"when":{"audio_codec":"opus"},"min":"96kbps","ideal":"128kbps","max":"160kbps"},{"when":{"audio_codec":"aac"},"min":"128kbps","ideal":"160kbps","max":"192kbps"},{"when":{"audio_codec":"mp3"},"min":"160kbps","ideal":"192kbps","max":"224kbps"}]}}',
    '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}'
  ),
  (
    'AAC 160/48kHz/16-bit (m4a)',
    '{"quality_profile":"AAC 160/48kHz/16-bit (m4a)","compare":["container","audio_codec","bitrate_distance","sample_rate_distance","audio_channels","bit_depth_distance","score"],"tiers":{"container":[["m4a"],["flac"],["mp3"]],"audio_codec":[["aac"],["alac","flac"],["mp3"]],"audio_channels":[["2.0"],["5.1"],["1.0"]]},"targets":{"bitrate":[{"min":"128kbps","ideal":"160kbps","max":"320kbps","weights":{"over":0.6,"under":1.0}}],"sample_rate":[{"min":"44100","ideal":"48000","max":"192000","weights":{"over":0.5,"under":1.0}}],"bit_depth":[{"min":"16","ideal":"16","max":"24","weights":{"over":0.5,"under":1.0}}]}}',
    '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}'
  );
