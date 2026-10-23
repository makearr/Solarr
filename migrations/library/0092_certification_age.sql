-- The youngest age a certification admits, read by the media type when the rating is written: -1 when the rating
-- is none or not understood, NULL until read (plan 10-libraries L2).
ALTER TABLE work_certification ADD COLUMN age INTEGER;
