-- What was last asked of a file, hashed (2026-09-29): a file is written again only when its ids,
-- its metadata, its cover or its library's tagging options change. Empty for rows written before.
ALTER TABLE tag_writeback ADD COLUMN digest TEXT NOT NULL DEFAULT '';
