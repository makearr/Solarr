-- A library's kind within its media type (TV: series, anime, documentary, reality, kids), chosen when it is added; it
-- brought the library's starting defaults and still decides what its media type derives from it. '' is none.
ALTER TABLE category ADD COLUMN kind TEXT NOT NULL DEFAULT '';
