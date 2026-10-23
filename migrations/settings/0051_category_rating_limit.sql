-- A library's maximum rating as the youngest age a work may admit, NULL for none; allow_unrated lets in a work
-- with no rating understood (plan 10-libraries L2).
ALTER TABLE category ADD COLUMN max_age INTEGER;
ALTER TABLE category ADD COLUMN allow_unrated INTEGER NOT NULL DEFAULT 0 CHECK (allow_unrated IN (0, 1));
