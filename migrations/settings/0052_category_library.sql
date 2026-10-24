-- A library holding several media types is its first category and one member category per further type, which
-- points at the first; a member's name, priority, rating limit, search share and access follow it (plan 10-libraries L4).
ALTER TABLE category ADD COLUMN library_id INTEGER REFERENCES category(id);
