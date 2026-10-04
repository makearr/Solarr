-- The constant rate factor an x264 or x265 encode states in its settings; NULL when the file states none.
ALTER TABLE media_attributes ADD COLUMN crf REAL;
