-- The library a hand-made collection belongs to (0 for one a provider names, which belongs to wherever its works are).
ALTER TABLE collection ADD COLUMN category_id INTEGER NOT NULL DEFAULT 0;
