-- A library's share of the automatic searches, weighed against the others' (user, 2026-10-23); 0 is none.
ALTER TABLE category ADD COLUMN search_share INTEGER NOT NULL DEFAULT 1 CHECK (search_share BETWEEN 0 AND 100);
