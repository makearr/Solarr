-- Video hashes now carry the algorithm that made them; the old ones (one uncropped frame per moment) are dropped so the probe sweep hashes every video again.
ALTER TABLE video_frame_hash ADD COLUMN algorithm_version INTEGER NOT NULL DEFAULT 0;
DELETE FROM video_frame_hash;
UPDATE media_object SET phash = NULL WHERE kind = 'video' AND phash IS NOT NULL;
