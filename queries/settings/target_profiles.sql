-- name: ListTargetProfiles :many
SELECT id, name, media_types, description, resolution, video_codec, frame_rate, max_frame_rate, crf, encoder_preset, encoder_tune,
       deinterlace, audio_codec, audio_channels, audio_bitrate_kbps, sample_rate_hz, bit_depth, bitrate_kbps, recommended_target, builtin
FROM target_profile ORDER BY name;

-- name: CreateTargetProfile :one
INSERT INTO target_profile (name, media_types, description, resolution, video_codec, frame_rate, max_frame_rate, crf, encoder_preset, encoder_tune,
  deinterlace, audio_codec, audio_channels, audio_bitrate_kbps, sample_rate_hz, bit_depth, bitrate_kbps, recommended_target)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: UpdateTargetProfile :exec
UPDATE target_profile SET name = ?, media_types = ?, description = ?, resolution = ?, video_codec = ?, frame_rate = ?, max_frame_rate = ?,
  crf = ?, encoder_preset = ?, encoder_tune = ?, deinterlace = ?, audio_codec = ?, audio_channels = ?, audio_bitrate_kbps = ?,
  sample_rate_hz = ?, bit_depth = ?, bitrate_kbps = ?, recommended_target = ?
WHERE id = ?;

-- name: DeleteTargetProfile :exec
DELETE FROM target_profile WHERE id = ?;
